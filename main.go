// webrtc-forward: bridge a WebRTC data channel to a local TCP host:port.
//
// Usage (interactive / browser):
//
//	webrtc-forward <host:port>
//
// Usage (HTTP signaling, for CLI client / automated testing):
//
//	webrtc-forward --signal :8765 <host:port>
//
// In --signal mode the tool serves:
//
//	POST /offer   body: SDP offer JSON → response: SDP answer JSON
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/pion/webrtc/v3"
)

func main() {
	signalAddr := flag.String("signal", "", "serve HTTP signaling on this address (e.g. :8765)")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [--signal <addr>] <host:port>\n", os.Args[0])
		os.Exit(1)
	}
	target := args[0]

	if *signalAddr != "" {
		runHTTPSignaling(*signalAddr, target)
		return
	}
	runStdin(target)
}

// runHTTPSignaling serves POST /offer for automated / CLI-client use.
func runHTTPSignaling(addr, target string) {
	fmt.Printf("HTTP signaling on %s → forwarding to %s\n", addr, target)
	http.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var offer webrtc.SessionDescription
		if err := json.Unmarshal(body, &offer); err != nil {
			http.Error(w, "bad offer JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		answer, err := handleOffer(offer, target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(answer)
	})
	if err := http.ListenAndServe(addr, nil); err != nil {
		fatal("HTTP signaling server", err)
	}
}

// runStdin loops reading SDP offers from stdin and printing answers.
func runStdin(target string) {
	for {
		fmt.Println("Paste the SDP offer JSON from the browser, then press Enter twice:")
		raw, err := readUntilBlank()
		if err != nil {
			fatal("reading offer", err)
		}
		if raw == "" {
			continue
		}
		var offer webrtc.SessionDescription
		if err := json.Unmarshal([]byte(raw), &offer); err != nil {
			fmt.Printf("bad offer JSON: %v — try again\n", err)
			continue
		}
		if offer.Type != webrtc.SDPTypeOffer {
			fmt.Printf("expected type \"offer\" but got %q — did you paste the answer instead of the offer? Try again\n", offer.Type)
			continue
		}
		answer, err := handleOffer(offer, target)
		if err != nil {
			fmt.Printf("error handling offer: %v — try again\n", err)
			continue
		}
		answerJSON, _ := json.Marshal(answer)
		fmt.Println("\nPaste this SDP answer into the browser:")
		fmt.Println(string(answerJSON))
		fmt.Println()
	}
}

// handleOffer creates a peer connection, sets the offer, creates and returns the answer.
// The connection is left running in the background.
func handleOffer(offer webrtc.SessionDescription, target string) (*webrtc.SessionDescription, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating peer connection: %w", err)
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		fmt.Printf("[dc] channel %q — connecting to %s\n", dc.Label(), target)
		dc.OnOpen(func() {
			conn, err := net.Dial("tcp", target)
			if err != nil {
				fmt.Printf("[dc] TCP dial failed: %v\n", err)
				dc.Close()
				return
			}
			fmt.Printf("[dc] bridged to %s\n", target)
			bridge(dc, conn)
		})
		dc.OnClose(func() { fmt.Println("[dc] closed") })
		dc.OnError(func(e error) { fmt.Printf("[dc] error: %v\n", e) })
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		return nil, fmt.Errorf("set remote description: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("create answer: %w", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("set local description: %w", err)
	}
	<-gatherDone
	local := pc.LocalDescription()
	return local, nil
}

// bridge forwards data between a WebRTC data channel and a TCP connection.
func bridge(dc *webrtc.DataChannel, conn net.Conn) {
	// TCP → data channel
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if sendErr := dc.Send(buf[:n]); sendErr != nil {
					fmt.Printf("[bridge] dc send: %v\n", sendErr)
					break
				}
			}
			if err != nil {
				if err != io.EOF {
					fmt.Printf("[bridge] TCP read: %v\n", err)
				}
				break
			}
		}
		dc.Close()
	}()
	// Data channel → TCP
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if _, err := conn.Write(msg.Data); err != nil {
			fmt.Printf("[bridge] TCP write: %v\n", err)
			conn.Close()
		}
	})
	dc.OnClose(func() { conn.Close() })
}

func readUntilBlank() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	var sb strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && sb.Len() > 0 {
			break
		}
		sb.WriteString(line)
	}
	return strings.TrimSpace(sb.String()), scanner.Err()
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error %s: %v\n", msg, err)
	os.Exit(1)
}
