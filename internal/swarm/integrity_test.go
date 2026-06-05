package swarm

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

func TestParseHashes(t *testing.T) {
	pl := []byte("#EXTM3U\n#EXTINF:4.0,\n#EXT-X-P2P-HASH:abc123\nseg00000.ts\n#EXTINF:4.0,\n#EXT-X-P2P-HASH:def456\nseg00001.ts\n")
	m := ParseHashes(pl)
	require.Equal(t, "abc123", m["seg00000.ts"])
	require.Equal(t, "def456", m["seg00001.ts"])
}

func TestVerifySegment(t *testing.T) {
	data := []byte("payload")
	sum := blake3.Sum256(data)
	good := hex.EncodeToString(sum[:])
	require.True(t, VerifySegment(data, good))
	require.False(t, VerifySegment(data, "deadbeef"))
	require.False(t, VerifySegment([]byte("tampered"), good))
}
