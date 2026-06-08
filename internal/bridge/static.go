package bridge

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var staticFS embed.FS

// SetBootstrap supplies the values injected into index.html for the SPA.
func (b *Bridge) SetBootstrap(nodeID, name string) {
	b.mu.Lock()
	b.selfNodeID, b.selfName = nodeID, name
	b.mu.Unlock()
}

// handleStatic serves the embedded SPA. Real asset files are served as-is; any
// other path falls back to index.html (client-side routing) with the token
// bootstrap injected.
func (b *Bridge) handleStatic(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	sub, _ := fs.Sub(staticFS, "dist")
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean != "" {
		if f, err := sub.Open(clean); err == nil {
			f.Close()
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
	}
	// fallback: index.html with bootstrap injected
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "no UI bundle", http.StatusInternalServerError)
		return
	}
	b.mu.Lock()
	boot := fmt.Sprintf(`<script>window.__P2P__=%s</script>`, b.bootstrapJSON())
	b.mu.Unlock()
	out := strings.Replace(string(data), "<!--__P2P_BOOTSTRAP__-->", boot, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// bootstrapJSON builds the injected window.__P2P__ object. json.Marshal
// HTML-escapes <, >, & by default, neutralizing a </script> breakout in any
// field. Caller holds b.mu.
func (b *Bridge) bootstrapJSON() string {
	out, err := json.Marshal(struct {
		Token  string `json:"token"`
		NodeID string `json:"nodeId"`
		Name   string `json:"name"`
	}{b.token, b.selfNodeID, b.selfName})
	if err != nil {
		return "{}"
	}
	return string(out)
}
