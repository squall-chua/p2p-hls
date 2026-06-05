package app

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

const swarmRendition = "v0" // single rendition this slice; ABR will key by real rendition

// swarmSession is the I/O shell around the pure swarm engine for one viewed party.
type swarmSession struct {
	t         swarmTransport
	self      identity.NodeID
	host      identity.NodeID
	contentID string
	// partyID is effectively immutable after construction: set once via setPartyID
	// before start()/publication to pc.swarm, so gossipLoop and FetchSegment read it
	// without ss.mu.
	partyID string
	clock   swarm.Clock
	cfg     swarm.Config

	mu        sync.Mutex
	eng       *swarm.Swarm
	cache     *segCache
	hashes    map[string]string // segName -> hex (from Host playlist)
	lastIdx   int               // most-recently-requested Segment index (window center)
	uploadSem chan struct{}
	stop      chan struct{}
}

func newSwarmSession(t swarmTransport, self, host identity.NodeID, contentID string,
	clk swarm.Clock, cfg swarm.Config) *swarmSession {
	return &swarmSession{
		t:         t,
		self:      self,
		host:      host,
		contentID: contentID,
		clock:     clk,
		cfg:       cfg,
		eng:       swarm.New(self, clk, cfg, rand.New(rand.NewSource(seedFor(self)))),
		cache:     newSegCache(),
		hashes:    map[string]string{},
		uploadSem: make(chan struct{}, cfg.UploadCap),
		stop:      make(chan struct{}),
	}
}

// seedFor derives a deterministic-per-Node RNG seed from the NodeID so different
// Nodes pick different random links while each Node is reproducible in tests.
func seedFor(id identity.NodeID) int64 {
	var h int64 = 1469598103934665603
	for _, b := range []byte(id) {
		h ^= int64(b)
		h *= 1099511628211
	}
	if h < 0 {
		h = -h
	}
	return h
}

func (ss *swarmSession) setPartyID(id string) { ss.mu.Lock(); ss.partyID = id; ss.mu.Unlock() }

// setPeers updates the engine's peer set from the party Audience.
func (ss *swarmSession) setPeers(members []identity.NodeID) {
	ss.mu.Lock()
	ss.eng.SetPeers(members)
	ss.mu.Unlock()
	for _, m := range members {
		if m != ss.self {
			_ = ss.t.ensurePeer(m)
		}
	}
}

// OnSwarmHave merges a peer's gossiped have-map.
func (ss *swarmSession) OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave) {
	ss.mu.Lock()
	ss.eng.OnPeerHave(remote, h.GetBaseIndex(), h.GetBitmap(), h.GetEpoch(), ss.clock.Now())
	ss.mu.Unlock()
}

// SwarmSegment serves a cached Segment to a peer, bounded by the upload cap.
func (ss *swarmSession) SwarmSegment(_ identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	idx, ok := swarm.SegIndex(req.GetSegName())
	if !ok {
		return nil, peer.ErrNotFound
	}
	select {
	case ss.uploadSem <- struct{}{}:
		defer func() { <-ss.uploadSem }()
	default:
		return nil, peer.ErrBusy
	}
	if b, ok := ss.cache.get(idx); ok {
		return b, nil
	}
	return nil, peer.ErrNotFound
}

// FetchSegment is the pull seam: serve from cache, else lowest-RTT verified peer,
// else the Host. Verified bytes are cached and advertised.
func (ss *swarmSession) FetchSegment(ctx context.Context, segName string) ([]byte, error) {
	idx, ok := swarm.SegIndex(segName)
	if !ok {
		return ss.t.hostSegment(ctx, ss.host, ss.contentID, segName)
	}
	ss.mu.Lock()
	ss.lastIdx = idx
	if b, ok := ss.cache.get(idx); ok {
		ss.mu.Unlock()
		return b, nil
	}
	ss.mu.Unlock()

	want := ss.expectedHash(ctx, segName)

	// Only pull from peers when we have a Host-published hash to verify against.
	// With no hash, peer bytes can't be verified, so fail closed: skip peers and
	// go straight to the Host (the trust origin, whose bytes are acceptable raw).
	if want != "" {
		for {
			ss.mu.Lock()
			src, ok := ss.eng.SelectSource(idx, ss.clock.Now())
			ss.mu.Unlock()
			if !ok {
				break
			}
			data, err := ss.t.fetchSwarmSegment(ctx, src, &peerv1.GetSwarmSegment{
				PartyId: ss.partyID, Rendition: swarmRendition, SegName: segName,
			})
			if err != nil {
				ss.mu.Lock()
				if isBusy(err) {
					ss.eng.MarkBusy(src, ss.clock.Now())
				} else {
					ss.eng.Demote(src)
				}
				ss.mu.Unlock()
				continue
			}
			if !swarm.VerifySegment(data, want) {
				ss.mu.Lock()
				ss.eng.Demote(src)
				ss.mu.Unlock()
				continue
			}
			ss.store(idx, data)
			return data, nil
		}
	}

	data, err := ss.t.hostSegment(ctx, ss.host, ss.contentID, segName)
	if err != nil {
		return nil, err
	}
	if want != "" && !swarm.VerifySegment(data, want) {
		return nil, peer.ErrUnavailable
	}
	ss.store(idx, data)
	return data, nil
}

func (ss *swarmSession) store(idx int, data []byte) {
	ss.cache.put(idx, data)
	ss.mu.Lock()
	ss.eng.SetHave(idx)
	lo := ss.lastIdx - ss.cfg.WindowLag
	hi := ss.lastIdx + ss.cfg.WindowLead
	if lo < 0 {
		lo = 0
	}
	ss.eng.Retain(lo, hi)
	ss.mu.Unlock()
	ss.cache.retain(lo, hi)
}

// expectedHash returns the Host-published hash for segName, refreshing from the Host
// playlist (the trust anchor) if not yet known.
func (ss *swarmSession) expectedHash(ctx context.Context, segName string) string {
	ss.mu.Lock()
	h, ok := ss.hashes[segName]
	ss.mu.Unlock()
	if ok {
		return h
	}
	pl, err := ss.t.hostPlaylist(ctx, ss.host, ss.contentID, "index.m3u8")
	if err != nil {
		return ""
	}
	parsed := swarm.ParseHashes(pl)
	ss.mu.Lock()
	for k, v := range parsed {
		ss.hashes[k] = v
	}
	h = ss.hashes[segName]
	ss.mu.Unlock()
	return h
}

func isBusy(err error) bool { return errors.Is(err, peer.ErrBusy) }

func (ss *swarmSession) start() { go ss.gossipLoop() }

func (ss *swarmSession) close() { close(ss.stop) }

func (ss *swarmSession) gossipLoop() {
	t := time.NewTicker(ss.cfg.GossipInterval)
	defer t.Stop()
	for {
		select {
		case <-ss.stop:
			return
		case <-t.C:
			ss.mu.Lock()
			ss.eng.ExpireStale(ss.clock.Now())
			base, bitmap, epoch := ss.eng.HaveMsg()
			targets := ss.eng.GossipTargets()
			ss.mu.Unlock()
			have := &peerv1.SwarmHave{
				PartyId: ss.partyID, Rendition: swarmRendition,
				BaseIndex: base, Bitmap: bitmap, Epoch: epoch,
			}
			for _, tgt := range targets {
				_ = ss.t.sendTo(tgt, &peerv1.Envelope{Body: &peerv1.Envelope_SwarmHave{SwarmHave: have}})
				if rtt, err := ss.t.measureRTT(context.Background(), tgt); err == nil {
					ss.mu.Lock()
					ss.eng.OnRTT(tgt, rtt)
					ss.mu.Unlock()
				}
			}
		}
	}
}
