// Command node runs a P2P HLS Node. For Slice 1 it connects to signaling, lists
// presence, and can dial+ping a peer by Node ID.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

func main() {
	name := flag.String("name", "anonymous", "display name")
	dial := flag.String("dial", "", "node id to dial and ping (optional)")
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

	time.Sleep(500 * time.Millisecond) // let presence settle

	if *dial == "" {
		fmt.Println("Connected. Online peers will appear as they join. (No --dial target given.)")
		time.Sleep(10 * time.Second)
		return
	}

	sess, err := node.Dial(ctx, identity.NodeID(*dial))
	if err != nil {
		fatal(err)
	}
	pong, err := sess.Ping(ctx, "hello")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("Ping OK, echoed nonce: %q\n", pong)
}

func fatal(err error) {
	slog.Error("node failed", "err", err)
	os.Exit(1)
}
