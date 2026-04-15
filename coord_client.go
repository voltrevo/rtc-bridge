package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"

	"webrtc-forward/protocol"
)

const (
	coordPingInterval  = 60 * time.Second
	coordReconnectWait = 5 * time.Second
)

// runCoordinator connects to a coordinator and handles offers indefinitely,
// reconnecting on disconnect until ctx is cancelled.
func runCoordinator(ctx context.Context, url string, id *Identity, services map[string]string) {
	svcNames := make([]string, 0, len(services))
	for k := range services {
		svcNames = append(svcNames, k)
	}
	for {
		err := connectCoordinator(ctx, url, id, svcNames, services)
		if ctx.Err() != nil {
			return // context cancelled — clean exit
		}
		fmt.Printf("[coord:%s] disconnected: %v — reconnecting in %s\n",
			url, err, coordReconnectWait)
		select {
		case <-time.After(coordReconnectWait):
		case <-ctx.Done():
			return
		}
	}
}

func connectCoordinator(ctx context.Context, url string, id *Identity, svcNames []string, services map[string]string) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Step 1: generate r_node, commit to it, send hello.
	rNode := make([]byte, 32)
	if _, err := rand.Read(rNode); err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	commitment := sha256.Sum256(rNode)
	hello := protocol.HelloMsg{
		Type:       protocol.MsgHello,
		NodeID:     id.NodeID,
		PublicKey:  []byte(id.PubKey),
		Commitment: commitment[:],
	}
	if err := conn.WriteJSON(hello); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	// Step 2: read challenge.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read challenge: %w", err)
	}
	var chal protocol.ChallengeMsg
	if err := json.Unmarshal(raw, &chal); err != nil || chal.Type != protocol.MsgChallenge {
		return fmt.Errorf("expected challenge, got: %s", raw)
	}

	// Step 3: send register with proof over joint random.
	reg := protocol.RegisterMsg{
		Type:      protocol.MsgRegister,
		NodeID:    id.NodeID,
		PublicKey: []byte(id.PubKey),
		RNode:     rNode,
		Proof:     protocol.SignRegistration(id.PrivKey, rNode, chal.RCoord),
		Services:  svcNames,
	}
	if err := conn.WriteJSON(reg); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	fmt.Printf("[coord:%s] registered as %s\n", url, id.NodeID)

	// Close conn when context is cancelled (triggers read loop to exit).
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(coordPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.WriteMessage(websocket.PingMessage, nil)
			case <-stopPing:
				return
			case <-ctx.Done():
				// Send graceful close before closing the connection.
				msg := protocol.CloseMsg{Type: protocol.MsgClose}
				raw, _ := json.Marshal(msg)
				conn.WriteMessage(websocket.TextMessage, raw)
				conn.Close()
				return
			}
		}
	}()

	// Read loop.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		if env.Type == protocol.MsgOffer {
			var msg protocol.OfferMsg
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			go handleCoordOffer(conn, msg, services)
		}
	}
}

func handleCoordOffer(conn *websocket.Conn, msg protocol.OfferMsg, services map[string]string) {
	var offer webrtc.SessionDescription
	if err := json.Unmarshal(msg.Offer, &offer); err != nil {
		fmt.Printf("[coord] bad offer JSON for request %s: %v\n", msg.RequestID, err)
		return
	}
	answer, err := handleOffer(offer, services)
	if err != nil {
		fmt.Printf("[coord] handleOffer error for request %s: %v\n", msg.RequestID, err)
		return
	}
	answerJSON, err := json.Marshal(answer)
	if err != nil {
		return
	}
	ans := protocol.AnswerMsg{
		Type:      protocol.MsgAnswer,
		RequestID: msg.RequestID,
		Answer:    json.RawMessage(answerJSON),
	}
	raw, _ := json.Marshal(ans)
	conn.WriteMessage(websocket.TextMessage, raw)
}
