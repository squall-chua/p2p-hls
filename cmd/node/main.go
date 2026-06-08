// Command node runs a P2P HLS Node: it connects to signaling and serves the
// loopback browser UI control plane (REST + SSE) from an embedded SPA.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

func main() {
	name := flag.String("name", "anonymous", "display name")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	flag.Parse()

	configDir, err := app.ConfigDir()
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		fatal(err)
	}
	id, err := identity.LoadOrCreate(filepath.Join(configDir, "identity.key"))
	if err != nil {
		fatal(err)
	}
	fmt.Println("Node ID:", id.NodeID())

	cfg, err := app.LoadConfig(filepath.Join(configDir, "config.toml"))
	if err != nil {
		fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	node, err := app.NewNode(ctx, id, *name, cfg)
	if err != nil {
		fatal(err)
	}
	defer node.Close()

	token := app.NewToken()
	br := bridge.New(node, token)
	br.SetControl(node)
	br.SetEvents(node.Events())
	br.SetBootstrap(string(id.NodeID()), *name)
	br.SetPartyHandler(node.PartyWS())
	if err := br.Start("127.0.0.1:0"); err != nil {
		fatal(err)
	}
	defer br.Close()

	url := br.BaseURL() + "/?token=" + token // dev/manual bootstrap convenience
	fmt.Println("UI ready:", url)
	if !*noOpen {
		_ = openBrowser(url)
	}
	select {} // serve until interrupted
}

// openBrowser opens url in the default browser (best-effort).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func fatal(err error) {
	slog.Error("node failed", "err", err)
	os.Exit(1)
}
