package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

func TestInjectHashesAddsTagBeforeEachSegment(t *testing.T) {
	dir := t.TempDir()
	seg := []byte("TSDATA-0")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seg00000.ts"), seg, 0o600))

	pl := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg00000.ts\n#EXT-X-ENDLIST\n")
	out := string(InjectHashes(pl, NewSegmentHashes(dir)))

	sum := blake3.Sum256(seg)
	wantHex := hexOf(sum[:])
	require.Contains(t, out, "#EXT-X-P2P-HASH:"+wantHex+"\nseg00000.ts\n")
	// Unknown lines and EXTINF order preserved.
	require.True(t, strings.Index(out, "#EXTINF:4.0,") < strings.Index(out, "#EXT-X-P2P-HASH:"))
}

func TestInjectHashesSkipsMissingSegment(t *testing.T) {
	dir := t.TempDir() // no seg file on disk
	pl := []byte("#EXTINF:4.0,\nseg00000.ts\n")
	out := string(InjectHashes(pl, NewSegmentHashes(dir)))
	require.NotContains(t, out, "#EXT-X-P2P-HASH")
	require.Contains(t, out, "seg00000.ts")
}

func hexOf(b []byte) string { return NewSegmentHashes("").encodeHex(b) }
