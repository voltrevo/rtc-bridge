// coordinator: discovery and signaling server for webrtc-forward nodes.
//
// Commands:
//
//	coordinator run  [--config path]   run the coordinator
//	coordinator init [--config path]   write a sample config file
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"rtc-mesh/protocol"
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
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  coordinator run  [--config path]   run the coordinator")
	fmt.Fprintln(os.Stderr, "  coordinator init [--config path]   generate a sample config file")
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	configPath := fs.String("config", "config.json5", "path to write sample config")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if _, err := os.Stat(*configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %q already exists\n", *configPath)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, []byte(sampleConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %q: %v\n", *configPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote sample config to %q\n", *configPath)
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "config.json5", "path to JSON5 config file")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	coord := newCoordinator()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", coord.handleNodeWS)
	mux.HandleFunc("/services", coord.handleServices)
	mux.HandleFunc("/offer", coord.handleOffer)

	fmt.Printf("coordinator listening on %s\n", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, corsMiddleware(mux)); err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
}

// corsMiddleware adds permissive CORS headers so browser clients on any origin
// can reach the coordinator's HTTP endpoints (/services, /offer).
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Coordinator ───────────────────────────────────────────────────────────────

const (
	nodePingInterval = 60 * time.Second
	nodeDropAfter    = 3 * time.Minute
)

type pendingOffer struct {
	ch chan protocol.AnswerMsg
}

type node struct {
	id       string
	services []string
	conn     *websocket.Conn
	lastSeen time.Time
	pending  map[string]*pendingOffer // requestId → waiter
	mu       sync.Mutex
}

type coordinator struct {
	mu    sync.RWMutex
	nodes map[string]*node // nodeId → node
}

func newCoordinator() *coordinator {
	c := &coordinator{nodes: make(map[string]*node)}
	go c.pruneLoop()
	return c
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (c *coordinator) handleNodeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	// Step 1: read hello (commitment).
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello protocol.HelloMsg
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Type != protocol.MsgHello {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "expected hello"))
		return
	}
	if !protocol.VerifyHello(&hello) {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "nodeId/publicKey mismatch"))
		return
	}

	// Step 2: send challenge (coordinator's random contribution).
	rCoord := make([]byte, 32)
	if _, err := rand.Read(rCoord); err != nil {
		return
	}
	challenge := protocol.ChallengeMsg{Type: protocol.MsgChallenge, RCoord: rCoord}
	if err := conn.WriteJSON(challenge); err != nil {
		return
	}

	// Step 3: read register (commitment opening + proof).
	_, raw, err = conn.ReadMessage()
	if err != nil {
		return
	}
	conn.SetReadDeadline(time.Time{})

	var reg protocol.RegisterMsg
	if err := json.Unmarshal(raw, &reg); err != nil || reg.Type != protocol.MsgRegister {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "expected register"))
		return
	}
	if !protocol.VerifyRegistration(&reg, hello.Commitment, rCoord) {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "invalid proof"))
		return
	}

	n := &node{
		id:       reg.NodeID,
		services: reg.Services,
		conn:     conn,
		lastSeen: time.Now(),
		pending:  make(map[string]*pendingOffer),
	}
	c.registerNode(n)
	defer c.unregisterNode(n.id)
	fmt.Printf("[node] registered %s services=%v\n", n.id, n.services)

	// Set up pong handler to update lastSeen.
	conn.SetPongHandler(func(string) error {
		n.mu.Lock()
		n.lastSeen = time.Now()
		n.mu.Unlock()
		return nil
	})

	// Read loop.
	for {
		conn.SetReadDeadline(time.Now().Add(nodeDropAfter))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		switch env.Type {
		case protocol.MsgAnswer:
			var ans protocol.AnswerMsg
			if err := json.Unmarshal(raw, &ans); err != nil {
				continue
			}
			n.mu.Lock()
			p, ok := n.pending[ans.RequestID]
			if ok {
				delete(n.pending, ans.RequestID)
			}
			n.mu.Unlock()
			if ok {
				p.ch <- ans
			}

		case protocol.MsgClose:
			fmt.Printf("[node] graceful close %s\n", n.id)
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

func (c *coordinator) registerNode(n *node) {
	c.mu.Lock()
	c.nodes[n.id] = n
	c.mu.Unlock()
}

func (c *coordinator) unregisterNode(id string) {
	c.mu.Lock()
	delete(c.nodes, id)
	c.mu.Unlock()
	fmt.Printf("[node] disconnected %s\n", id)
}

func (c *coordinator) pruneLoop() {
	for range time.Tick(30 * time.Second) {
		cutoff := time.Now().Add(-nodeDropAfter)
		c.mu.Lock()
		for id, n := range c.nodes {
			n.mu.Lock()
			stale := n.lastSeen.Before(cutoff)
			n.mu.Unlock()
			if stale {
				fmt.Printf("[node] pruning stale %s\n", id)
				n.conn.Close()
				delete(c.nodes, id)
			}
		}
		c.mu.Unlock()
	}
}

// pingLoop is started per-node by the node's writer goroutine (unused here —
// the read loop's deadline handles drops; we rely on WS ping/pong).

// ── HTTP handlers ─────────────────────────────────────────────────────────────

// GET /services → {"echo":["nodeId1","nodeId2"],...}
func (c *coordinator) handleServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	svcMap := make(map[string][]string)
	c.mu.RLock()
	for id, n := range c.nodes {
		for _, svc := range n.services {
			svcMap[svc] = append(svcMap[svc], id)
		}
	}
	c.mu.RUnlock()
	for _, ids := range svcMap {
		sort.Strings(ids)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(svcMap)
}

// POST /offer  body: {service, nodeId, offer}
// Returns the answer from the node.
func (c *coordinator) handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Service string          `json:"service"`
		NodeID  string          `json:"nodeId"`
		Offer   json.RawMessage `json:"offer"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.NodeID == "" || req.Service == "" || len(req.Offer) == 0 {
		http.Error(w, "service, nodeId, and offer are required", http.StatusBadRequest)
		return
	}

	c.mu.RLock()
	n, ok := c.nodes[req.NodeID]
	c.mu.RUnlock()
	if !ok {
		http.Error(w, fmt.Sprintf("node %q not found", req.NodeID), http.StatusNotFound)
		return
	}

	// Check node offers the requested service.
	serviced := false
	for _, s := range n.services {
		if s == req.Service {
			serviced = true
			break
		}
	}
	if !serviced {
		http.Error(w, fmt.Sprintf("node %q does not offer service %q", req.NodeID, req.Service), http.StatusBadRequest)
		return
	}

	requestID := uuid.NewString()
	p := &pendingOffer{ch: make(chan protocol.AnswerMsg, 1)}

	n.mu.Lock()
	n.pending[requestID] = p
	n.mu.Unlock()

	// Forward offer to node.
	offerMsg := protocol.OfferMsg{
		Type:      protocol.MsgOffer,
		RequestID: requestID,
		Offer:     req.Offer,
	}
	raw, _ := json.Marshal(offerMsg)
	n.mu.Lock()
	err = n.conn.WriteMessage(websocket.TextMessage, raw)
	n.mu.Unlock()
	if err != nil {
		n.mu.Lock()
		delete(n.pending, requestID)
		n.mu.Unlock()
		http.Error(w, "failed to forward offer to node", http.StatusBadGateway)
		return
	}

	// Wait for answer (30s timeout).
	select {
	case ans := <-p.ch:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ans.Answer)
	case <-time.After(30 * time.Second):
		n.mu.Lock()
		delete(n.pending, requestID)
		n.mu.Unlock()
		http.Error(w, "timeout waiting for answer from node", http.StatusGatewayTimeout)
	}
}

const sampleConfig = `{
  // Address to listen on.
  addr: "0.0.0.0:8765",
}
`
