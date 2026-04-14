// cli-client: WebRTC client that connects to a webrtc-forward HTTP signaling
// endpoint, opens a data channel, sends test messages and prints responses.
//
// Usage:
//
//	cli-client [--signal http://127.0.0.1:8765] [--messages "hello,world"]
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
	signalURL := flag.String("signal", "http://127.0.0.1:8765", "webrtc-forward HTTP signaling URL (base)")
	messages := flag.String("messages", "hello,ping,goodbye", "comma-separated messages to send")
	flag.Parse()

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
	fmt.Printf("[client] sending offer to %s/offer\n", *signalURL)

	// POST offer to webrtc-forward's HTTP signaling endpoint.
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

	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		fatal("decoding answer", err)
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

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error %s: %v\n", msg, err)
	os.Exit(1)
}
