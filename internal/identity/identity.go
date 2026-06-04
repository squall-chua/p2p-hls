// Package identity provides a Node's Ed25519 keypair and its derived Node ID.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NodeID is the canonical, stable address of a Node: a fingerprint of its public key.
type NodeID string

// Identity is a Node's keypair.
type Identity struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   NodeID
}

// Generate creates a fresh random identity.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return &Identity{priv: priv, pub: pub, id: NodeIDFromPublicKey(pub)}, nil
}

// LoadOrCreate loads the identity seed from path, or generates and persists one (0600).
func LoadOrCreate(path string) (*Identity, error) {
	seed, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("identity file %s: bad seed length %d", path, len(seed))
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub, ok := priv.Public().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("identity file %s: unexpected public key type", path)
		}
		return &Identity{priv: priv, pub: pub, id: NodeIDFromPublicKey(pub)}, nil
	case errors.Is(err, fs.ErrNotExist):
		id, gerr := Generate()
		if gerr != nil {
			return nil, gerr
		}
		if mkerr := os.MkdirAll(filepath.Dir(path), 0o700); mkerr != nil {
			return nil, fmt.Errorf("create identity dir: %w", mkerr)
		}
		if werr := os.WriteFile(path, id.priv.Seed(), 0o600); werr != nil {
			return nil, fmt.Errorf("write identity: %w", werr)
		}
		return id, nil
	default:
		return nil, fmt.Errorf("read identity %s: %w", path, err)
	}
}

// NodeID returns the Node's stable identifier.
func (i *Identity) NodeID() NodeID { return i.id }

// PublicKey returns the Node's Ed25519 public key.
func (i *Identity) PublicKey() ed25519.PublicKey {
	cp := make(ed25519.PublicKey, len(i.pub))
	copy(cp, i.pub)
	return cp
}

// Sign signs msg with the Node's private key.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.priv, msg) }

// NodeIDFromPublicKey derives the Node ID from a public key:
// lowercase, unpadded base32 of SHA-256(pubkey).
func NodeIDFromPublicKey(pub ed25519.PublicKey) NodeID {
	sum := sha256.Sum256(pub)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return NodeID(strings.ToLower(enc.EncodeToString(sum[:])))
}

// Verify reports whether sig is a valid signature of msg by pub.
func Verify(pub ed25519.PublicKey, msg, sig []byte) bool {
	return ed25519.Verify(pub, msg, sig)
}
