// Command signal-server runs the trust-minimized WebRTC signaling server.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/squall-chua/p2p-hls/internal/signalserver"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	srv := signalserver.New()
	http.HandleFunc("/ws", srv.HandleWS)
	slog.Info("signaling server listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
