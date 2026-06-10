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
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
)

func main() {
	name := flag.String("name", "anonymous", "display name")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	bridgeAddr := flag.String("bridge-addr", "127.0.0.1:0", "loopback bridge bind address")
	configFlag := flag.String("config-dir", "", "base dir for identity, config.toml, library db + cache (default: OS config dir)")
	flag.Parse()

	configDir := *configFlag
	if configDir == "" {
		var err error
		configDir, err = app.ConfigDir()
		if err != nil {
			fatal(err)
		}
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

	// Library + catalog + media: make this node serve its own content.
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = configDir
	}
	store, err := library.OpenStore(filepath.Join(dataDir, "library.db"))
	if err != nil {
		fatal(err)
	}
	defer store.Close()

	vis := catalog.VisibilityRestricted
	if cfg.DefaultVisibility == "public" {
		vis = catalog.VisibilityPublic
	}
	policy := catalog.NewPolicy(vis)
	for _, a := range cfg.AllowList {
		policy.AddAllow(identity.NodeID(a))
	}
	for _, b := range cfg.BlockList {
		policy.AddBlock(identity.NodeID(b))
	}
	cacheDir := filepath.Join(dataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		fatal(err)
	}

	catalogSvc := catalog.NewService(store, policy, catalog.NewRequests(), cacheDir, cfg.SharedFolders)
	node.SetCatalog(catalogSvc)

	node.SetMedia(media.NewService(media.NewEngine(store, media.ExecRunner{}, cacheDir), policy))

	// Index shared folders so the Library is populated before the UI loads.
	if len(cfg.SharedFolders) > 0 {
		scanCtx, scanCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		scanner := library.NewScanner(store, library.FFProbe{}, cfg.SharedFolders).
			SetThumbnailer(cacheDir, library.FFThumbnailer{})
		if serr := scanner.ScanOnce(scanCtx); serr != nil {
			slog.Warn("library scan failed", "err", serr)
		}
		scanCancel()
		titles, _ := store.All()
		fmt.Printf("Library: %d title(s) indexed from %v\n", len(titles), cfg.SharedFolders)
		// Live-watch shared folders so new/changed files appear without a restart.
		go func() {
			if werr := scanner.Watch(context.Background()); werr != nil {
				slog.Warn("library watch stopped", "err", werr)
			}
		}()
	}

	token := app.NewToken()
	br := bridge.New(node, token)
	br.SetControl(node)
	br.SetEvents(node.Events())
	br.SetBootstrap(string(id.NodeID()), *name)
	br.SetPartyHandler(node.PartyWS())
	if err := br.Start(*bridgeAddr); err != nil {
		fatal(err)
	}
	defer br.Close()

	// Open the bare URL: the served page injects window.__P2P__ (carrying the
	// token) same-origin, so the token stays out of browser history. The
	// ?token= URL is the dev fallback for `nuxt dev` proxy mode; print it.
	fmt.Println("UI ready:", br.BaseURL())
	fmt.Println("Dev URL (nuxt dev):", br.BaseURL()+"/?token="+token)
	if !*noOpen {
		_ = openBrowser(br.BaseURL())
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
