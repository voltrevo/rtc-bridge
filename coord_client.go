package main

import (
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
// reconnecting on disconnect.
func runCoordinator(url string, id *Identity, services map[string]string) {
	svcNames := make([]string, 0, len(services))
	for k := range services {
		svcNames = append(svcNames, k)
	}
	for {
		err := connectCoordinator(url, id, svcNames, services)
		fmt.Printf("[coord:%s] disconnected: %v — reconnecting in %s\n",
			url, err, coordReconnectWait)
		time.Sleep(coordReconnectWait)
	}
}

func connectCoordinator(url string, id *Identity, svcNames []string, services map[string]string) error {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Send registration.
	reg := protocol.RegisterMsg{
		Type:      protocol.MsgRegister,
		NodeID:    id.NodeID,
		PublicKey: []byte(id.PubKey),
		Proof:     id.Sign([]byte(id.NodeID)),
		Services:  svcNames,
	}
	if err := conn.WriteJSON(reg); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	fmt.Printf("[coord:%s] registered as %s\n", url, id.NodeID)

	// Ping loop.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(coordPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.WriteMessage(websocket.PingMessage, nil)
			case <-done:
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

// SendClose sends a graceful close message to the coordinator.
func sendClose(conn *websocket.Conn) {
	msg := protocol.CloseMsg{Type: protocol.MsgClose}
	raw, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, raw)
}
