package peer

import (
	"context"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

type fakePartyHandler struct {
	states    chan *peerv1.PartyState
	joinedCID string
}

func (f *fakePartyHandler) OnJoinParty(_ identity.NodeID, contentID string) (*peerv1.PartyWelcome, error) {
	f.joinedCID = contentID
	return &peerv1.PartyWelcome{PartyId: "p1", Initial: &peerv1.PartyState{PartyId: "p1", PositionMs: 42}}, nil
}
func (f *fakePartyHandler) OnLeaveParty(identity.NodeID, string)                       {}
func (f *fakePartyHandler) OnPartyState(_ identity.NodeID, s *peerv1.PartyState)       { f.states <- s }
func (f *fakePartyHandler) OnPartyAudience(identity.NodeID, *peerv1.PartyAudience)     {}
func (f *fakePartyHandler) OnPartyInvite(identity.NodeID, *peerv1.PartyInvite)         {}
func (f *fakePartyHandler) OnPartyEnded(identity.NodeID, *peerv1.PartyEnded)           {}

func TestPartyStateDeliveredToHandler(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	h := &fakePartyHandler{states: make(chan *peerv1.PartyState, 1)}
	b.SetPartyHandler(h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.SendControl(&peerv1.Envelope{
		Body: &peerv1.Envelope_PartyState{PartyState: &peerv1.PartyState{PartyId: "p1", PositionMs: 1234, Seq: 7}},
	}))
	select {
	case s := <-h.states:
		require.Equal(t, int64(1234), s.GetPositionMs())
		require.Equal(t, uint64(7), s.GetSeq())
	case <-ctx.Done():
		t.Fatal("PartyState not delivered")
	}
}

func TestJoinPartyRequestResponse(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	h := &fakePartyHandler{states: make(chan *peerv1.PartyState, 1)}
	b.SetPartyHandler(h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := a.JoinParty(ctx, "cid-xyz")
	require.NoError(t, err)
	require.Equal(t, "p1", w.GetPartyId())
	require.Equal(t, int64(42), w.GetInitial().GetPositionMs())
	require.Equal(t, "cid-xyz", h.joinedCID)
}

func TestCapabilityAdvertised(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	require.Eventually(t, func() bool { return a.HasCapability(CapParty) && b.HasCapability(CapParty) },
		3*time.Second, 25*time.Millisecond)
}

func TestMeasureRTTPositive(t *testing.T) {
	a, _, _ := connectPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rtt, err := a.MeasureRTT(ctx)
	require.NoError(t, err)
	require.Positive(t, rtt)
}
