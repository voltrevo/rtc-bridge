// cli-client: WebRTC client that connects to a rtc-bridge HTTP signaling
// endpoint, opens a data channel, sends test messages and prints responses.
//
// Usage:
//
//	cli-client [--signal http://127.0.0.1:8765] --service NAME [--messages "hello,world"]
//	cli-client --coordinator http://coord:8765  --service NAME [--node nodeId] [--messages "hello,world"]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pion/webrtc/v3"
)

func main() {
	fs := flag.NewFlagSet("cli-client", flag.ContinueOnError)
	signalURL := fs.String("signal", "http://127.0.0.1:8765", "rtc-bridge HTTP signaling URL (base)")
	coordURL := fs.String("coordinator", "", "coordinator base URL (uses /services + /offer instead of --signal)")
	nodeID := fs.String("node", "", "nodeId to connect to (optional when using --coordinator; auto-picks first if blank)")
	service := fs.String("service", "", "service name to request (required)")
	messages := fs.String("messages", "hello,ping,goodbye", "comma-separated messages to send")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if *service == "" {
		fmt.Fprintln(os.Stderr, "error: --service is required")
		os.Exit(1)
	}

	msgs := strings.Split(*messages, ",")

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		fatal("creating peer connection", err)
	}

	// Received messages channel.
	received := make(chan string, 32)

	dc, err := pc.CreateDataChannel("forward", nil)
	if err != nil {
		fatal("creating data channel", err)
	}

	dcOpen := make(chan struct{})
	dc.OnOpen(func() { close(dcOpen) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		received <- string(msg.Data)
	})
	dc.OnError(func(e error) { fmt.Printf("[client] dc error: %v\n", e) })

	// Create offer.
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		fatal("creating offer", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		fatal("set local description", err)
	}
	<-gatherDone

	offerJSON, _ := json.Marshal(pc.LocalDescription())

	var answer webrtc.SessionDescription
	if *coordURL != "" {
		answer = postViaCoordinator(*coordURL, *service, *nodeID, offerJSON)
	} else {
		fmt.Printf("[client] sending offer to %s/offer\n", *signalURL)
		resp, err := http.Post(*signalURL+"/offer", "application/json", bytes.NewReader(offerJSON))
		if err != nil {
			fatal("posting offer", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "signal server returned %d: %s\n", resp.StatusCode, body)
			os.Exit(1)
		}
		if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
			fatal("decoding answer", err)
		}
	}
	if err := pc.SetRemoteDescription(answer); err != nil {
		fatal("set remote description", err)
	}
	fmt.Println("[client] answer set, waiting for data channel...")

	// Wait for data channel to open.
	select {
	case <-dcOpen:
		fmt.Println("[client] data channel open")
	case <-time.After(15 * time.Second):
		fmt.Fprintln(os.Stderr, "[client] timeout waiting for data channel")
		os.Exit(1)
	}

	// Handshake: send service name, await "ok" or "err: ...".
	fmt.Printf("[client] requesting service %q\n", *service)
	if err := dc.SendText(*service); err != nil {
		fmt.Printf("[client] handshake send error: %v\n", err)
		os.Exit(1)
	}
	select {
	case ack := <-received:
		if ack != "ok" {
			fmt.Fprintf(os.Stderr, "[client] handshake failed: %s\n", ack)
			os.Exit(1)
		}
		fmt.Println("[client] handshake ok")
	case <-time.After(10 * time.Second):
		fmt.Fprintln(os.Stderr, "[client] timeout waiting for handshake ack")
		os.Exit(1)
	}

	// Send messages and collect responses.
	ok := true
	for _, msg := range msgs {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		fmt.Printf("[client] → %q\n", msg)
		if err := dc.SendText(msg + "\n"); err != nil {
			fmt.Printf("[client] send error: %v\n", err)
			ok = false
			continue
		}
		select {
		case reply := <-received:
			fmt.Printf("[client] ← %q\n", reply)
		case <-time.After(5 * time.Second):
			fmt.Printf("[client] timeout waiting for reply to %q\n", msg)
			ok = false
		}
		time.Sleep(50 * time.Millisecond)
	}

	dc.Close()
	pc.Close()

	if ok {
		fmt.Println("\nPASS")
	} else {
		fmt.Fprintln(os.Stderr, "\nFAIL")
		os.Exit(1)
	}
}

// postViaCoordinator resolves a nodeId (or picks one) and posts offer via coordinator.
func postViaCoordinator(coordBase, service, nodeID string, offerJSON []byte) webrtc.SessionDescription {
	if nodeID == "" {
		// GET /services → pick first node offering this service.
		resp, err := http.Get(coordBase + "/services")
		if err != nil {
			fatal("GET /services", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "coordinator /services returned %d: %s\n", resp.StatusCode, body)
			os.Exit(1)
		}
		var svcMap map[string][]string
		if err := json.NewDecoder(resp.Body).Decode(&svcMap); err != nil {
			fatal("decoding /services", err)
		}
		nodes, ok := svcMap[service]
		if !ok || len(nodes) == 0 {
			fmt.Fprintf(os.Stderr, "error: no nodes offering service %q\n", service)
			os.Exit(1)
		}
		nodeID = nodes[0]
		fmt.Printf("[client] auto-selected node %s for service %q\n", nodeID, service)
	}

	type offerReq struct {
		Service string          `json:"service"`
		NodeID  string          `json:"nodeId"`
		Offer   json.RawMessage `json:"offer"`
	}
	body, _ := json.Marshal(offerReq{Service: service, NodeID: nodeID, Offer: json.RawMessage(offerJSON)})
	fmt.Printf("[client] posting offer via coordinator %s/offer (node %s)\n", coordBase, nodeID)

	resp, err := http.Post(coordBase+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		fatal("posting offer to coordinator", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "coordinator /offer returned %d: %s\n", resp.StatusCode, b)
		os.Exit(1)
	}
	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		fatal("decoding coordinator answer", err)
	}
	return answer
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error %s: %v\n", msg, err)
	os.Exit(1)
}
