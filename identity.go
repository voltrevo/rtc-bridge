package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"

	"webrtc-forward/protocol"
)

// Identity holds the node's long-term keypair and derived nodeId.
type Identity struct {
	PrivKey ed25519.PrivateKey
	PubKey  ed25519.PublicKey
	NodeID  string
}

// LoadOrCreateIdentity reads an existing key file or generates a new keypair.
// The file stores the 64-byte ed25519 private key (seed + public key).
func LoadOrCreateIdentity(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("identity file %q has unexpected size %d (want %d)",
				path, len(data), ed25519.PrivateKeySize)
		}
		priv := ed25519.PrivateKey(data)
		pub := priv.Public().(ed25519.PublicKey)
		return &Identity{PrivKey: priv, PubKey: pub, NodeID: protocol.NodeID(pub)}, nil
	}

	// Generate new keypair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}
	if err := os.WriteFile(path, []byte(priv), 0600); err != nil {
		return nil, fmt.Errorf("writing identity file %q: %w", path, err)
	}
	fmt.Printf("generated new node identity: %s (saved to %q)\n", protocol.NodeID(pub), path)
	return &Identity{PrivKey: priv, PubKey: pub, NodeID: protocol.NodeID(pub)}, nil
}

// Sign signs data with the node's private key.
func (id *Identity) Sign(data []byte) []byte {
	return ed25519.Sign(id.PrivKey, data)
}
