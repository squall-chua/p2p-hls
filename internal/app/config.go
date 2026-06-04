// Package app wires Nodes together and loads configuration.
package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config is the Node's settings, loaded from config.toml.
type Config struct {
	DisplayName  string   `toml:"display_name"`
	SignalingURL string   `toml:"signaling_url"`
	STUNServers  []string `toml:"stun_servers"`
}

func defaults() Config {
	return Config{
		DisplayName:  "anonymous",
		SignalingURL: "ws://localhost:8080/ws",
		STUNServers:  []string{"stun:stun.l.google.com:19302"},
	}
}

// LoadConfig reads path over the defaults. A missing file yields pure defaults.
func LoadConfig(path string) (Config, error) {
	cfg := defaults()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ConfigDir returns the per-user config directory for the app.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "p2p-hls"), nil
}
