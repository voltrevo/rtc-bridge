// Package protocol defines the WebSocket message types and nodeId computation
// shared between webrtc-forward nodes and the coordinator.
package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"math/big"
)

// MsgType identifies a WebSocket message.
type MsgType string

const (
	MsgRegister MsgType = "register"
	MsgOffer    MsgType = "offer"
	MsgAnswer   MsgType = "answer"
	MsgClose    MsgType = "close"
)

// Envelope is used to peek at the type field before full decode.
type Envelope struct {
	Type MsgType `json:"type"`
}

// RegisterMsg is the first message sent by a node after connecting.
type RegisterMsg struct {
	Type      MsgType         `json:"type"`
	NodeID    string          `json:"nodeId"`
	PublicKey []byte          `json:"publicKey"` // raw ed25519 public key (32 bytes)
	Proof     []byte          `json:"proof"`     // ed25519.Sign(privKey, []byte(nodeId))
	Services  []string        `json:"services"`
}

// OfferMsg is sent by the coordinator to a node.
type OfferMsg struct {
	Type      MsgType         `json:"type"`
	RequestID string          `json:"requestId"`
	Offer     json.RawMessage `json:"offer"`
}

// AnswerMsg is sent by a node back to the coordinator.
type AnswerMsg struct {
	Type      MsgType         `json:"type"`
	RequestID string          `json:"requestId"`
	Answer    json.RawMessage `json:"answer"`
}

// CloseMsg is sent by a node for a graceful shutdown.
type CloseMsg struct {
	Type MsgType `json:"type"`
}

// NodeID computes the node identifier from an ed25519 public key:
// base36(sha256(pubKey))[:30]
func NodeID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	n := new(big.Int).SetBytes(sum[:])
	s := n.Text(36)
	if len(s) > 30 {
		return s[:30]
	}
	return s
}

// VerifyRegistration checks that the nodeId matches the public key and that
// the proof is a valid signature of the nodeId bytes.
func VerifyRegistration(msg *RegisterMsg) bool {
	pub := ed25519.PublicKey(msg.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	if NodeID(pub) != msg.NodeID {
		return false
	}
	return ed25519.Verify(pub, []byte(msg.NodeID), msg.Proof)
}
