// webrtc-forward: bridge a WebRTC data channel to a local TCP host:port.
//
// Commands:
//
//	webrtc-forward run  [--config path]   run the forwarder (default config: config.json5)
//	webrtc-forward init [--config path]   write a sample config file and exit
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
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  webrtc-forward run  [--config path]   run the forwarder\n")
	fmt.Fprintf(os.Stderr, "  webrtc-forward init [--config path]   generate a sample config file\n")
}

// ── Commands ──────────────────────────────────────────────────────────────────

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "config.json5", "path to JSON5 config file")
	fs.Parse(args)

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("target: %s\n", cfg.Target)
	switch cfg.Signaling.Type {
	case "http":
		runHTTPSignaling(cfg.Signaling.Addr, cfg.Target)
	default:
		runStdin(cfg.Target)
	}
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "config.json5", "path to write sample config")
	fs.Parse(args)

	if _, err := os.Stat(*configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %q already exists — delete it first or choose a different path\n", *configPath)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, []byte(SampleConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %q: %v\n", *configPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote sample config to %q\n", *configPath)
}

// ── Signaling modes ───────────────────────────────────────────────────────────

func runHTTPSignaling(addr, target string) {
	fmt.Printf("signaling: http %s\n", addr)
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
		if offer.Type != webrtc.SDPTypeOffer {
			http.Error(w, fmt.Sprintf(`expected type "offer", got %q`, offer.Type), http.StatusBadRequest)
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
		fmt.Fprintf(os.Stderr, "http signaling: %v\n", err)
		os.Exit(1)
	}
}

func runStdin(target string) {
	fmt.Println("signaling: stdin")
	for {
		fmt.Println("\nPaste the SDP offer JSON from the browser, then press Enter twice:")
		raw, err := readUntilBlank()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
			os.Exit(1)
		}
		if raw == "" {
			continue
		}
		var offer webrtc.SessionDescription
		if err := json.Unmarshal([]byte(raw), &offer); err != nil {
			fmt.Printf("error: bad offer JSON: %v — try again\n", err)
			continue
		}
		if offer.Type != webrtc.SDPTypeOffer {
			fmt.Printf("error: expected type \"offer\" but got %q — did you paste the answer instead of the offer? Try again\n", offer.Type)
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
	}
}

// ── WebRTC ────────────────────────────────────────────────────────────────────

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

		// Buffer messages that arrive before the TCP connection is ready.
		buf := make(chan []byte, 256)
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			data := make([]byte, len(msg.Data))
			copy(data, msg.Data)
			buf <- data
		})
		dc.OnClose(func() {
			fmt.Println("[dc] closed")
			close(buf)
		})
		dc.OnError(func(e error) { fmt.Printf("[dc] error: %v\n", e) })

		dc.OnOpen(func() {
			conn, err := net.Dial("tcp", target)
			if err != nil {
				fmt.Printf("[dc] TCP dial failed: %v\n", err)
				dc.Close()
				return
			}
			fmt.Printf("[dc] bridged to %s\n", target)
			bridge(dc, conn, buf)
		})
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

func bridge(dc *webrtc.DataChannel, conn net.Conn, buf chan []byte) {
	// Drain messages buffered before TCP connection was ready, then forward
	// ongoing messages from the same channel.
	go func() {
		for data := range buf {
			if _, err := conn.Write(data); err != nil {
				fmt.Printf("[bridge] TCP write: %v\n", err)
				conn.Close()
				return
			}
		}
		conn.Close()
	}()

	// Forward TCP→DataChannel.
	go func() {
		rbuf := make([]byte, 65536)
		for {
			n, err := conn.Read(rbuf)
			if n > 0 {
				if sendErr := dc.Send(rbuf[:n]); sendErr != nil {
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
