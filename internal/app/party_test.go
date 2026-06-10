package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestPartyCloseStopsHeartbeat(t *testing.T) {
	pc := newPartyCoordinator(nil, "host", party.RealClock(), party.DefaultConfig())
	pc.StartParty("cid")
	pc.mu.Lock()
	stop := pc.stopHB
	pc.mu.Unlock()
	require.NotNil(t, stop)

	pc.close()

	select {
	case <-stop:
	default:
		t.Fatal("heartbeat stop channel not closed by close()")
	}
	pc.mu.Lock()
	require.Nil(t, pc.stopHB)
	pc.mu.Unlock()
}

func TestPartyCloseStopsSwarmGossip(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	ss := newSwarmSession(&fakeTransport{}, "self", "host", "cid", swarm.RealClock(), swarm.DefaultConfig())
	ss.start()
	pc.mu.Lock()
	pc.swarm = ss
	pc.mu.Unlock()

	pc.close()

	select {
	case <-ss.stop:
	default:
		t.Fatal("swarm gossip stop channel not closed by close()")
	}
	pc.mu.Lock()
	require.Nil(t, pc.swarm)
	pc.mu.Unlock()
}

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

func TestLeavePartyIgnoresStalePartyEnded(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.beginViewer("host1")
	fired := make(chan struct{}, 1)
	pc.onPartyEnded = func() { fired <- struct{}{} }
	pc.LeaveParty()
	// A late PartyEnded from the host we already left must not fire the callback.
	pc.OnPartyEnded("host1", &peerv1.PartyEnded{})
	select {
	case <-fired:
		t.Fatal("stale PartyEnded fired onPartyEnded after leave")
	default:
	}
}

func TestOnPartyEndedIgnoresDuplicate(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.beginViewer("host1")
	count := 0
	pc.onPartyEnded = func() { count++ }
	pc.OnPartyEnded("host1", &peerv1.PartyEnded{})
	pc.OnPartyEnded("host1", &peerv1.PartyEnded{}) // duplicate/stale from the old host
	require.Equal(t, 1, count, "onPartyEnded must fire exactly once")
}

func TestCurrentPartyHost(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.StartParty("movie1")
	pc.OnJoinParty("alice", "movie1")
	cp := pc.currentParty()
	require.True(t, cp.active)
	require.Equal(t, "host", cp.role)
	require.Equal(t, identity.NodeID("self"), cp.host)
	require.Equal(t, "movie1", cp.contentID)
	require.Equal(t, 1, cp.viewers)
}

func TestCurrentPartyNoneWhenIdle(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	require.False(t, pc.currentParty().active)
}

func TestCurrentPartyViewer(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.beginViewer("host1")
	cp := pc.currentParty()
	require.True(t, cp.active)
	require.Equal(t, "viewer", cp.role)
	require.Equal(t, identity.NodeID("host1"), cp.host)
}

func TestCurrentPartyViewerClearedAfterLeave(t *testing.T) {
	pc := newPartyCoordinator(nil, "self", party.RealClock(), party.DefaultConfig())
	pc.beginViewer("host1")
	pc.LeaveParty()
	require.False(t, pc.currentParty().active)
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

// stepClock is a frozen, manually-advanced clock for deterministic rate-gate tests.
type stepClock struct{ t time.Time }

func (c *stepClock) Now() time.Time          { return c.t }
func (c *stepClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// captureSender records every Envelope sent, per destination node.
type captureSender struct {
	mu   sync.Mutex
	sent []capturedEnv
}
type capturedEnv struct {
	to  identity.NodeID
	env *peerv1.Envelope
}

func (c *captureSender) sendTo(to identity.NodeID, env *peerv1.Envelope) error {
	c.mu.Lock()
	c.sent = append(c.sent, capturedEnv{to, env})
	c.mu.Unlock()
	return nil
}
func (c *captureSender) measureRTT(context.Context, identity.NodeID) (time.Duration, error) {
	return 0, nil
}

// danmakusTo counts PartyDanmaku envelopes addressed to `node`.
func (c *captureSender) danmakusTo(node identity.NodeID) []*peerv1.PartyDanmaku {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*peerv1.PartyDanmaku
	for _, s := range c.sent {
		if s.to == node {
			if d := s.env.GetPartyDanmaku(); d != nil {
				out = append(out, d)
			}
		}
	}
	return out
}

func TestHostBroadcastsDanmakuToAllMembers(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")
	pc.OnJoinParty("bob", "cid")
	pc.out = make(chan []byte, 4) // local sink so the host sees its own

	pc.OnPartyDanmaku("alice", &peerv1.PartyDanmaku{Text: "  hello  "})

	for _, who := range []identity.NodeID{"alice", "bob"} {
		ds := cs.danmakusTo(who)
		require.Len(t, ds, 1)
		require.Equal(t, "hello", ds[0].GetText())         // trimmed
		require.Equal(t, "alice", ds[0].GetSenderNodeId()) // Host-stamped identity
	}
	require.NotEmpty(t, pc.out) // host's own browser was pushed to
}

func TestHostDropsDanmakuFromNonAudienceSender(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	pc.OnPartyDanmaku("mallory", &peerv1.PartyDanmaku{Text: "spoof"}) // not in Audience

	require.Empty(t, cs.danmakusTo("alice"))
}

func TestHostRateLimitsDanmaku(t *testing.T) {
	cs := &captureSender{}
	clk := &stepClock{t: time.Unix(1_700_000_000, 0)} // frozen => no refill
	pc := newPartyCoordinator(cs, identity.NodeID("host"), clk, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	for i := 0; i < 5; i++ {
		pc.OnPartyDanmaku("alice", &peerv1.PartyDanmaku{Text: "x"})
	}
	require.Len(t, cs.danmakusTo("alice"), 3) // burst of 3, rest throttled
}

func TestHandlePlayerDanmakuHostBroadcasts(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	pc.handlePlayerDanmaku("hey")

	ds := cs.danmakusTo("alice")
	require.Len(t, ds, 1)
	require.Equal(t, "hey", ds[0].GetText())
	require.Equal(t, "host", ds[0].GetSenderNodeId()) // host originates as itself
}

func TestHandlePlayerDanmakuViewerSendsToHostOnly(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("self"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host1"))

	pc.handlePlayerDanmaku("hi there")

	all := cs.danmakusTo("host1")
	require.Len(t, all, 1)
	require.Equal(t, "hi there", all[0].GetText())
	require.Empty(t, all[0].GetSenderNodeId()) // viewer leaves identity for the Host to stamp
}

func TestViewerPushesDanmakuFromHostOnly(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("self"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host1"))
	pc.out = make(chan []byte, 4)

	pc.OnPartyDanmaku("host1", &peerv1.PartyDanmaku{Text: "hi", SenderDisplay: "host1"})
	require.Len(t, pc.out, 1)

	pc.OnPartyDanmaku("stranger", &peerv1.PartyDanmaku{Text: "no"}) // not our host
	require.Len(t, pc.out, 1)                                       // unchanged
}
