package peer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

// fakeHandler answers RPCs for the Host side of a test.
type fakeHandler struct {
	mu          sync.Mutex
	allowed     bool
	titles      []*peerv1.TitleMeta
	liveParties []*peerv1.LivePartyMeta
	requested   chan string
}

// allow marks the handler allowed and sets the titles it will serve.
func (h *fakeHandler) allow(titles ...*peerv1.TitleMeta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allowed = true
	h.titles = titles
}

func (h *fakeHandler) Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.allowed {
		return nil, ErrDenied
	}
	return h.titles, nil
}
func (h *fakeHandler) GetMetadata(remote identity.NodeID, id string) (*peerv1.TitleMeta, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.titles {
		if t.GetContentId() == id {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (h *fakeHandler) LiveParties(remote identity.NodeID) ([]*peerv1.LivePartyMeta, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.allowed {
		return nil, ErrDenied
	}
	return h.liveParties, nil
}
func (h *fakeHandler) RequestAccess(remote identity.NodeID, msg string) error {
	h.requested <- msg
	return nil
}

func connectPair(t *testing.T) (viewer, host *Session, hostHandler *fakeHandler) {
	t.Helper()
	cfg := webrtc.Configuration{}
	sig := &directSignaler{dest: map[identity.NodeID]*Session{}}
	idV, _ := identity.Generate()
	idH, _ := identity.Generate()

	viewer, err := NewSession(idV, idH.NodeID(), cfg, sig)
	require.NoError(t, err)
	host, err = NewSession(idH, idV.NodeID(), cfg, sig)
	require.NoError(t, err)

	hostHandler = &fakeHandler{requested: make(chan string, 1)}
	host.SetHandler(hostHandler)

	sig.register(idV.NodeID(), viewer)
	sig.register(idH.NodeID(), host)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, host.Start(ctx, false))
	require.NoError(t, viewer.Start(ctx, true))
	select {
	case <-viewer.Ready():
	case <-ctx.Done():
		t.Fatal("not ready")
	}
	return viewer, host, hostHandler
}

func TestBrowseDeniedThenAllowed(t *testing.T) {
	viewer, _, h := connectPair(t)
	ctx := context.Background()

	_, err := viewer.Browse(ctx)
	require.ErrorIs(t, err, ErrDenied)

	h.allow(&peerv1.TitleMeta{ContentId: "cid1", DisplayTitle: "Movie"})
	got, err := viewer.Browse(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Movie", got[0].GetDisplayTitle())
}

func TestGetMetadataNotFound(t *testing.T) {
	viewer, _, h := connectPair(t)
	h.allow()
	_, err := viewer.GetMetadata(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLivePartiesDeniedThenAllowed(t *testing.T) {
	viewer, _, h := connectPair(t)
	ctx := context.Background()

	_, err := viewer.LiveParties(ctx)
	require.ErrorIs(t, err, ErrDenied)

	h.mu.Lock()
	h.allowed = true
	h.liveParties = []*peerv1.LivePartyMeta{{ContentId: "cid1", Viewers: 4}}
	h.mu.Unlock()

	got, err := viewer.LiveParties(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "cid1", got[0].GetContentId())
	require.Equal(t, int32(4), got[0].GetViewers())
}

func TestRequestAccessThenAccessGranted(t *testing.T) {
	viewer, host, h := connectPair(t)
	var granted sync.WaitGroup
	granted.Add(1)
	viewer.OnAccessGranted(func(identity.NodeID) { granted.Done() })

	require.NoError(t, viewer.RequestAccess(context.Background(), "please?"))
	require.Equal(t, "please?", <-h.requested)

	require.NoError(t, host.SendAccessGranted())
	done := make(chan struct{})
	go func() { granted.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("viewer never received AccessGranted")
	}
}
