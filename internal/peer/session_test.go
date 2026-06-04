package peer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

// directSignaler delivers signals straight to a peer's HandleSignal (no server),
// so we can test the session in isolation.
type directSignaler struct {
	mu   sync.Mutex
	dest map[identity.NodeID]*Session
}

func (d *directSignaler) register(id identity.NodeID, s *Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dest[id] = s
}

func (d *directSignaler) SendSignal(to identity.NodeID, s SignedSignal) error {
	d.mu.Lock()
	dst := d.dest[to]
	d.mu.Unlock()
	return dst.HandleSignal(s)
}

func TestTwoSessionsHandshakeAndPing(t *testing.T) {
	cfg := webrtc.Configuration{} // loopback: host candidates only, no STUN needed
	sig := &directSignaler{dest: map[identity.NodeID]*Session{}}

	idA, _ := identity.Generate()
	idB, _ := identity.Generate()

	sessA, err := NewSession(idA, idB.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessA.Close()
	sessB, err := NewSession(idB, idA.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessB.Close()

	sig.register(idA.NodeID(), sessA)
	sig.register(idB.NodeID(), sessB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, sessB.Start(ctx, false)) // answerer first (waits for offer)
	require.NoError(t, sessA.Start(ctx, true))  // initiator

	select {
	case <-sessA.Ready():
	case <-ctx.Done():
		t.Fatal("session A never became ready")
	}

	pong, err := sessA.Ping(ctx, "nonce-123")
	require.NoError(t, err)
	require.Equal(t, "nonce-123", pong)
}

func TestPingErrorsAndCleansUpWhenNoResponse(t *testing.T) {
	cfg := webrtc.Configuration{}
	sig := &directSignaler{dest: map[identity.NodeID]*Session{}}
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()

	sessA, err := NewSession(idA, idB.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessA.Close()
	sessB, err := NewSession(idB, idA.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessB.Close()

	sig.register(idA.NodeID(), sessA)
	sig.register(idB.NodeID(), sessB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, sessB.Start(ctx, false))
	require.NoError(t, sessA.Start(ctx, true))
	select {
	case <-sessA.Ready():
	case <-ctx.Done():
		t.Fatal("session A never became ready")
	}

	// Close B so it can never answer the ping.
	require.NoError(t, sessB.Close())

	// Ping with a short deadline: this exits via either the send-error path
	// (channel closing) or the ctx-deadline path — both must return an error
	// and clean up the pending entry.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer pingCancel()
	_, perr := sessA.Ping(pingCtx, "no-answer")
	require.Error(t, perr)

	sessA.mu.Lock()
	pendingLen := len(sessA.pending)
	sessA.mu.Unlock()
	require.Zero(t, pendingLen, "pending map must be cleaned up after a failed ping")
}
