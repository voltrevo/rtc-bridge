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
	MsgHello     MsgType = "hello"
	MsgChallenge MsgType = "challenge"
	MsgRegister  MsgType = "register"
	MsgOffer     MsgType = "offer"
	MsgAnswer    MsgType = "answer"
	MsgClose     MsgType = "close"
)

// Envelope is used to peek at the type field before full decode.
type Envelope struct {
	Type MsgType `json:"type"`
}

// HelloMsg is the first message sent by a node.
// It commits to r_node before seeing the coordinator's random value.
type HelloMsg struct {
	Type       MsgType `json:"type"`
	NodeID     string  `json:"nodeId"`
	PublicKey  []byte  `json:"publicKey"`  // raw ed25519 public key (32 bytes)
	Commitment []byte  `json:"commitment"` // sha256(r_node)
}

// ChallengeMsg is sent by the coordinator in response to HelloMsg.
type ChallengeMsg struct {
	Type   MsgType `json:"type"`
	RCoord []byte  `json:"rCoord"` // 32 random bytes from coordinator
}

// RegisterMsg is sent by the node after receiving the challenge.
// joint = r_node XOR r_coord; proof = Sign(privKey, signPayload(joint))
type RegisterMsg struct {
	Type      MsgType         `json:"type"`
	NodeID    string          `json:"nodeId"`
	PublicKey []byte          `json:"publicKey"`
	RNode     []byte          `json:"rNode"`    // opening of commitment
	Proof     []byte          `json:"proof"`    // Sign(privKey, signPayload(r_node XOR r_coord))
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

// signPayload builds the domain-separated payload that the node signs:
// "webrtc-forward:register:" || joint
func signPayload(joint []byte) []byte {
	prefix := []byte("webrtc-forward:register:")
	payload := make([]byte, len(prefix)+len(joint))
	copy(payload, prefix)
	copy(payload[len(prefix):], joint)
	return payload
}

// xor32 XORs two 32-byte slices and returns the result.
func xor32(a, b []byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// SignRegistration produces the proof for a RegisterMsg given r_node and r_coord.
func SignRegistration(privKey ed25519.PrivateKey, rNode, rCoord []byte) []byte {
	joint := xor32(rNode, rCoord)
	return ed25519.Sign(privKey, signPayload(joint))
}

// VerifyHello checks that the nodeId is consistent with the publicKey.
func VerifyHello(msg *HelloMsg) bool {
	pub := ed25519.PublicKey(msg.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	return NodeID(pub) == msg.NodeID
}

// VerifyRegistration checks the commitment opening and the proof.
// commitment is from the HelloMsg; rCoord is what the coordinator sent.
func VerifyRegistration(msg *RegisterMsg, commitment, rCoord []byte) bool {
	if len(msg.RNode) != 32 || len(rCoord) != 32 {
		return false
	}
	// Verify commitment opening.
	got := sha256.Sum256(msg.RNode)
	if len(commitment) != 32 {
		return false
	}
	for i := range got {
		if got[i] != commitment[i] {
			return false
		}
	}
	// Verify signature over joint random.
	pub := ed25519.PublicKey(msg.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	joint := xor32(msg.RNode, rCoord)
	return ed25519.Verify(pub, signPayload(joint), msg.Proof)
}
