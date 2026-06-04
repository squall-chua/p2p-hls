package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

func TestHashFileIsStableAndContentAddressed(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	require.NoError(t, os.WriteFile(a, []byte("identical bytes"), 0o600))
	require.NoError(t, os.WriteFile(b, []byte("identical bytes"), 0o600))

	ha, err := library.HashFile(a)
	require.NoError(t, err)
	hb, err := library.HashFile(b)
	require.NoError(t, err)
	require.Equal(t, ha, hb, "same content => same Content ID")
	require.Len(t, ha, 64) // 32-byte BLAKE3 hex

	require.NoError(t, os.WriteFile(b, []byte("different"), 0o600))
	hb2, err := library.HashFile(b)
	require.NoError(t, err)
	require.NotEqual(t, ha, hb2)
}
