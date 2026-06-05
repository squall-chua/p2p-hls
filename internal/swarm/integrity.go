package swarm

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/zeebo/blake3"
)

const hashTag = "#EXT-X-P2P-HASH:"

// ParseHashes extracts segName -> hex BLAKE3 from a Host-served media playlist. A
// hash tag applies to the next Segment URI line.
func ParseHashes(playlist []byte) map[string]string {
	out := map[string]string{}
	var pending string
	for _, ln := range strings.Split(string(playlist), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, hashTag):
			pending = strings.TrimPrefix(t, hashTag)
		case strings.HasSuffix(t, ".ts") && !strings.HasPrefix(t, "#"):
			if pending != "" {
				out[t] = pending
				pending = ""
			}
		}
	}
	return out
}

// VerifySegment reports whether data's BLAKE3-256 equals expectHex (constant-time).
func VerifySegment(data []byte, expectHex string) bool {
	want, err := hex.DecodeString(expectHex)
	if err != nil || len(want) == 0 {
		return false
	}
	sum := blake3.Sum256(data)
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}
