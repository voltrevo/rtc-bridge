// webrtc-forward: bridge a WebRTC data channel to a local TCP host:port.
//
// Commands:
//
//	webrtc-forward run  [--config path]   run the forwarder (default config: config.json5)
//	webrtc-forward init [--config path]   write a sample config file and exit
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

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

	if len(cfg.Services) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no services configured — all connections will be rejected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	id := IdentityFromPrivKey(cfg.PrivKey)
	fmt.Printf("node id: %s\n", id.NodeID)

	var wg sync.WaitGroup
	for _, coordURL := range cfg.Coordinators {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			runCoordinator(ctx, u, id, cfg.Services)
		}(coordURL)
	}

	wg.Wait()
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "config.json5", "path to write sample config")
	fs.Parse(args)

	if _, err := os.Stat(*configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %q already exists — delete it first or choose a different path\n", *configPath)
		os.Exit(1)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, []byte(GenerateSampleConfig(priv)), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %q: %v\n", *configPath, err)
		os.Exit(1)
	}
	id := IdentityFromPrivKey(priv)
	fmt.Printf("wrote config to %q\n", *configPath)
	fmt.Printf("node id: %s\n", id.NodeID)
}

// ── WebRTC ────────────────────────────────────────────────────────────────────

func handleOffer(offer webrtc.SessionDescription, services map[string]string) (*webrtc.SessionDescription, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating peer connection: %w", err)
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		// Buffer all incoming messages (first = service name, rest = TCP data).
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
			firstMsg, ok := <-buf
			if !ok {
				return
			}
			svcName := string(firstMsg)
			target, exists := services[svcName]
			if !exists {
				dc.SendText(fmt.Sprintf("err: unknown service %q", svcName))
				dc.Close()
				return
			}
			conn, err := net.Dial("tcp", target)
			if err != nil {
				fmt.Printf("[dc] TCP dial failed for service %q: %v\n", svcName, err)
				dc.SendText(fmt.Sprintf("err: dial failed: %v", err))
				dc.Close()
				return
			}
			dc.SendText("ok")
			fmt.Printf("[dc] service %q → %s\n", svcName, target)
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

