package app

import (
	"crypto/rand"
	"encoding/hex"
)

// NewToken returns a random 32-hex-char session token for the loopback bridge.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
