package signalserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/signaling"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

// dialAndRegister connects a raw WS client, completes challenge/register, and
// returns the connection plus the first presence snapshot.
func dialAndRegister(t *testing.T, wsURL string, id *identity.Identity, name string) (*websocket.Conn, *signaling.PresenceSnapshot) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	typ, env, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeChallenge, typ)
	ch := env.(*signaling.Challenge)

	reg, err := signaling.Marshal(signaling.Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: name,
		Signature:   id.Sign(ch.Nonce),
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, reg))

	_, msg, err = conn.ReadMessage()
	require.NoError(t, err)
	typ, env, err = signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypePresenceSnapshot, typ)
	return conn, env.(*signaling.PresenceSnapshot)
}

func newServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestRegisterThenPresenceJoinSeenByExistingPeer(t *testing.T) {
	wsURL := newServer(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()

	connA, snapA := dialAndRegister(t, wsURL, idA, "alice")
	require.Empty(t, snapA.Peers, "first peer sees nobody")

	_, snapB := dialAndRegister(t, wsURL, idB, "bob")
	require.Len(t, snapB.Peers, 1)
	require.Equal(t, string(idA.NodeID()), snapB.Peers[0].NodeID)

	// A should receive a PresenceJoin for B.
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := connA.ReadMessage()
	require.NoError(t, err)
	typ, env, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypePresenceJoin, typ)
	require.Equal(t, string(idB.NodeID()), env.(*signaling.PresenceJoin).Peer.NodeID)
}

func TestRelayForwardsPayloadWithFromSet(t *testing.T) {
	wsURL := newServer(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	connA, _ := dialAndRegister(t, wsURL, idA, "alice")
	connB, _ := dialAndRegister(t, wsURL, idB, "bob")

	out, err := signaling.Marshal(signaling.Relay{
		To:      string(idB.NodeID()),
		Payload: []byte("ping-payload"),
	})
	require.NoError(t, err)
	require.NoError(t, connA.WriteMessage(websocket.TextMessage, out))

	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := connB.ReadMessage()
		require.NoError(t, err)
		typ, env, err := signaling.Unmarshal(msg)
		require.NoError(t, err)
		if typ == signaling.TypeRelay {
			r := env.(*signaling.Relay)
			require.Equal(t, string(idA.NodeID()), r.From)
			require.Equal(t, []byte("ping-payload"), r.Payload)
			return
		}
	}
}

func TestRegisterRejectsBadSignature(t *testing.T) {
	wsURL := newServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_, _, err = conn.ReadMessage() // challenge
	require.NoError(t, err)
	id, _ := identity.Generate()
	reg, _ := signaling.Marshal(signaling.Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: "mallory",
		Signature:   []byte("not-a-valid-signature"),
	})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, reg))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	typ, _, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeError, typ)
}
