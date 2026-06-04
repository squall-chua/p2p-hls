package app

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorHostLifecycleAndProvider(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("host"), party.RealClock(), party.DefaultConfig())

	// No party yet => provider reports not live.
	live, n := pc.LiveParty("cid")
	require.False(t, live)
	require.Equal(t, 0, n)

	pid := pc.StartParty("cid")
	require.NotEmpty(t, pid)

	// A remote viewer joins via the inbound handler path.
	w, err := pc.OnJoinParty(identity.NodeID("alice"), "cid")
	require.NoError(t, err)
	require.Equal(t, pid, w.GetPartyId())

	live, n = pc.LiveParty("cid")
	require.True(t, live)
	require.Equal(t, 1, n)

	// Joining a content with no live party is rejected.
	_, err = pc.OnJoinParty(identity.NodeID("bob"), "other")
	require.Error(t, err)

	pc.OnLeaveParty(identity.NodeID("alice"), pid)
	_, n = pc.LiveParty("cid")
	require.Equal(t, 0, n)
}

func TestCoordinatorViewerIngestsState(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("viewer"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host"), "p1")
	pc.OnPartyState(identity.NodeID("host"), &peerv1.PartyState{PartyId: "p1", Playing: false, PositionMs: 7_000, Seq: 1})

	// The viewer engine should now want to seek a far-off player to 7_000.
	act := pc.viewerDecide(0, false, time.Now())
	require.True(t, act.Seek)
	require.Equal(t, int64(7_000), act.SeekMS)
}
