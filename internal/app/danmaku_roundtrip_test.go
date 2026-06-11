package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

// TestServeWSHostDanmakuRoundTrip exercises the REAL loopback WS round-trip that
// the unit tests bypass: hello -> serveWS registers the sink -> serveHostWS routes
// the danmaku -> broadcastDanmaku -> pushDanmaku -> writer goroutine -> conn.
func TestServeWSHostDanmakuRoundTrip(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("host"), party.RealClock(), party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		pc.serveWS(conn)
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer c.Close()

	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","role":"host"}`)))
	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte(`{"type":"danmaku","text":"hi there"}`)))

	require.NoError(t, c.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, msg, err := c.ReadMessage()
	require.NoError(t, err)
	var got danmakuPush
	require.NoError(t, json.Unmarshal(msg, &got))
	require.Equal(t, "danmaku", got.Type)
	require.Equal(t, "hi there", got.Text)
}
