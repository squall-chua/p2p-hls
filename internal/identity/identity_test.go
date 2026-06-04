package identity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestNodeIDIsStableFingerprintOfPublicKey(t *testing.T) {
	id, err := identity.Generate()
	require.NoError(t, err)
	require.Equal(t, id.NodeID(), identity.NodeIDFromPublicKey(id.PublicKey()))
	require.Len(t, string(id.NodeID()), 52) // base32(sha256) unpadded
}

func TestSignVerifyRoundTrip(t *testing.T) {
	id, err := identity.Generate()
	require.NoError(t, err)
	msg := []byte("hello watch party")
	sig := id.Sign(msg)
	require.True(t, identity.Verify(id.PublicKey(), msg, sig))
	require.False(t, identity.Verify(id.PublicKey(), []byte("tampered"), sig))

	other, err := identity.Generate()
	require.NoError(t, err)
	require.False(t, identity.Verify(other.PublicKey(), msg, sig))
}

func TestLoadOrCreatePersistsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	first, err := identity.LoadOrCreate(path)
	require.NoError(t, err)
	second, err := identity.LoadOrCreate(path)
	require.NoError(t, err)
	require.Equal(t, first.NodeID(), second.NodeID(), "reloading must yield the same identity")

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
