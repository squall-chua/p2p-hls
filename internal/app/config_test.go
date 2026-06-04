package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`display_name = "alice"`+"\n"), 0o600))

	cfg, err := app.LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "alice", cfg.DisplayName)
	require.Equal(t, "ws://localhost:8080/ws", cfg.SignalingURL) // default
	require.NotEmpty(t, cfg.STUNServers)                         // default
}

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := app.LoadConfig(filepath.Join(t.TempDir(), "nope.toml"))
	require.NoError(t, err)
	require.Equal(t, "ws://localhost:8080/ws", cfg.SignalingURL)
}
