package bridge_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/stretchr/testify/require"
)

func TestPartyWSUpgradesAndEchoes(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	got := make(chan string, 1)
	b.SetPartyHandler(func(c *websocket.Conn) {
		_, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		got <- string(msg)
		_ = c.WriteMessage(websocket.TextMessage, []byte("ack"))
	})
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()

	url := "ws" + strings.TrimPrefix(b.BaseURL(), "http") + "/party/secret-token"
	c, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://127.0.0.1"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer c.Close()

	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte("hello")))
	require.Equal(t, "hello", <-got)
	_, ack, err := c.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "ack", string(ack))
}

func TestPartyWSRejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetPartyHandler(func(*websocket.Conn) {})
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	url := "ws" + strings.TrimPrefix(b.BaseURL(), "http") + "/party/wrong"
	_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://127.0.0.1"}})
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_ = time.Second
}
