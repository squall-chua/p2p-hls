package app

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/squall-chua/p2p-hls/internal/peer"
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

func TestCoordinatorJoinDeniedByPolicy(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("host"), party.RealClock(), party.DefaultConfig())
	pc.StartParty("cid")
	pc.setAllowed(func(n identity.NodeID) bool { return n == identity.NodeID("alice") })

	_, err := pc.OnJoinParty(identity.NodeID("mallory"), "cid")
	require.ErrorIs(t, err, peer.ErrDenied)

	w, err := pc.OnJoinParty(identity.NodeID("alice"), "cid")
	require.NoError(t, err)
	require.NotEmpty(t, w.GetPartyId())

	_, err = pc.OnJoinParty(identity.NodeID("alice"), "unknown")
	require.ErrorIs(t, err, peer.ErrNotFound)
}

func TestAudienceViewHostSide(t *testing.T) {
	pc := newPartyCoordinator(nil, "host", party.RealClock(), party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.EndParty("")
	if _, err := pc.OnJoinParty("viewer1", "cid"); err != nil {
		t.Fatalf("join: %v", err)
	}
	av := pc.audienceView()
	if len(av) != 1 || av[0].GetNodeId() != "viewer1" {
		t.Fatalf("audienceView %+v", av)
	}
}

func TestOnPartyEndedFiresCallback(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.beginViewer("host1")
	fired := make(chan struct{}, 1)
	pc.onPartyEnded = func() { fired <- struct{}{} }
	pc.OnPartyEnded("host1", &peerv1.PartyEnded{})
	select {
	case <-fired:
	default:
		t.Fatal("onPartyEnded not fired")
	}
}

func TestCoordinatorViewerIngestsState(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("viewer"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host"))
	pc.OnPartyState(identity.NodeID("host"), &peerv1.PartyState{PartyId: "p1", Playing: false, PositionMs: 7_000, Seq: 1})

	// The viewer engine should now want to seek a far-off player to 7_000.
	act := pc.viewerDecide(0, false, time.Now())
	require.True(t, act.Seek)
	require.Equal(t, int64(7_000), act.SeekMS)
}
