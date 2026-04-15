package main

import (
	"crypto/ed25519"

	"rtc-mesh/protocol"
)

// Identity holds the node's long-term keypair and derived nodeId.
type Identity struct {
	PrivKey ed25519.PrivateKey
	PubKey  ed25519.PublicKey
	NodeID  string
}

// IdentityFromPrivKey derives an Identity from an existing private key.
func IdentityFromPrivKey(priv ed25519.PrivateKey) *Identity {
	pub := priv.Public().(ed25519.PublicKey)
	return &Identity{PrivKey: priv, PubKey: pub, NodeID: protocol.NodeID(pub)}
}

// Sign signs data with the node's private key.
func (id *Identity) Sign(data []byte) []byte {
	return ed25519.Sign(id.PrivKey, data)
}
