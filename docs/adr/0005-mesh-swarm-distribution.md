# Mesh swarm: party-scoped gossip distribution with Host-anchored Segment integrity

**Status:** accepted

A Watch Party today has every Viewer pull every Segment 1:1 from the Host, so the Host's
upload scales with the Audience size. The Swarm lets Viewers re-serve cached Segments to one
another to share that load. Four decisions define its shape. They are interdependent and are
recorded together.

## Decision 1 — The Swarm is scoped to one Watch Party's Audience; the Host-served playlist is the integrity trust anchor

Sharing is confined to the Viewers of a single Watch Party, all consuming the same Host's
transcode. The Host computes a BLAKE3 hash of each Segment as ffmpeg produces it and injects it
into the media playlist it serves (a custom `#EXT-X-P2P-HASH` tag per Segment). A Viewer always
fetches the playlist — and therefore the hashes — **directly from the Host**, never from a peer.
Segment *bytes* may come from any peer and are verified against the Host's hash before being
cached, served to the bridge, or re-served.

**Why:**
- A Content ID identifies the *source file*, not the streamed bytes. The Segments are a live
  ffmpeg transcode and are not byte-reproducible across Hosts (encoder non-determinism, version
  drift, segmentation boundaries, MPEG-TS muxing). So Segment bytes are identical only among the
  Viewers of *one* Host — exactly the Audience. Pooling across Hosts is impossible without
  content-addressed Segments.
- WebRTC (SCTP/DTLS) already protects against wire corruption. The threat the Swarm introduces is
  an untrusted *sender* — a peer serving poisoned, truncated, cross-transcode, or wrong-index
  bytes. A per-Segment hash from a trusted authority defeats this (the BitTorrent piece model).
- The playlist is small and infrequent; keeping it Host-only costs almost no offload while making
  the Host the single trust root. Hashes can only be published incrementally (the transcode is
  live, not upfront), and the growing playlist is the natural incremental carrier.

**Rejected alternative:** a general, title-wide swarm across Hosts of the same Content ID. It would
require deterministic transcoding (version-locked, single-threaded, still defeated by muxing) or
hash-on-produce dedup — fragile, slow, and a much larger project — for a gain we do not need.

**Consequences:**
- A poison or have-map lie is contained: verify-fail or `ErrNotFound`/timeout demotes the peer and
  the fetch retries the next candidate, then the Host. Liveness is always backstopped by the Host.
- hls.js ignores unknown playlist tags, so playback is unaffected by the hash tag.

## Decision 2 — The data-routing plane is a gossip mesh, even for friend-sized parties

Viewers epidemically gossip compact have-maps (which windowed Segments they hold) over peer
sessions — periodic, randomized, partial-view, with anti-entropy — and pull Segment bytes on
demand from a peer that advertises them. The Host coordinates neither routing nor membership of
the data plane.

**Why:**
- Chosen for decentralization on principle: no single coordinator in the data path, no O(N²)
  have-map broadcast, closer to a real swarm. Parties are expected to stay small, so this is a
  deliberate architectural choice rather than a scaling necessity.
- Pull-on-demand fits the existing `bridge.Streamer` pull seam and synced demand (everyone wants
  the same window), and needs no rarest-first piece selection — demand order equals playback order,
  and the rarest Segment is always the newest one, which the Host originates and the Swarm then
  propagates.

**Rejected alternatives:** full mesh with local decisions (simplest, but O(N²) connections and a
broadcast have-map, and a single visibility model rather than gossip); Host-as-tracker (re-centralizes
routing and adds a round-trip per fetch); push-based epidemic broadcast (wastes bandwidth pushing
Segments peers do not need and fights the pull seam).

**Consequences:**
- The gossip + selection logic lives in a pure engine (`internal/swarm`) driven by an injected
  `Clock` and RNG, tested deterministically with no network — matching the slice-4 sync engine
  discipline. Gossip target selection keeps at least one uniformly-random peer per round so
  proximity bias cannot cluster the mesh and stall convergence.

## Decision 3 — Connection setup is decentralized; glare is resolved by lower-NodeID-dials over the signaling relay

A Viewer↔Viewer connection is initiated by the peer with the lexicographically lower `NodeID`; the
higher waits for the inbound offer. When the peer that wants the edge is the higher `NodeID`, it
sends a "dial-me" nudge via a new opaque signaling-relay payload, prompting the lower peer to dial.
The Host is not involved.

**Why:**
- A mesh means two Viewers may offer to each other at once; today only one side ever dials
  (`internal/app/node.go` NOTE at the `Dial` glare hazard). A deterministic initiator removes the
  double-negotiation hazard without glare rollback.
- The signaling server already relays opaque node→node payloads to any pair in Presence (it carries
  SDP/ICE today), so the nudge needs no Host mediation. This keeps the Host out of the connection
  plane as well as the data plane — the Host's only roles are membership broadcast (the existing
  `PartyAudience`), Segment origin, and hash authority.

**Rejected alternative:** routing the dial nudge through the Host's party control channel — less code,
but it puts the Host back into connection setup, weakening the decentralization this slice is for.

## Decision 4 — Source selection is RTT-aware; the Host is the last-resort source

For a pull, candidates are peers whose latest have-map includes the Segment and that are not at
their upload cap; they are ranked by RTT ascending and the lowest is chosen, falling through to the
Host only when no peer can serve. Each Viewer caps concurrent outbound relays and rejects excess
with `ErrBusy`. There are no incentives or tit-for-tat.

**Why:**
- RTT is already measured by the ADR-0004 `Ping`/`Pong` clock; reusing it costs nothing and lowest-
  latency delivery is unambiguously good for the synced playback buffer. Greedy-low-latency is safe
  for pulls; gossip stays latency-*biased* (plus one random) to preserve mixing.
- Deprioritizing the Host to last-resort is what actually produces the offload. The Host fallback
  makes the whole scheme degrade gracefully: a Viewer with no reachable peers (e.g. behind a
  symmetric NAT, since viewer↔viewer TURN is out of scope) simply pulls from the Host as today.
- Incentives are unnecessary for liveness given the Host backstop, so they are omitted (YAGNI).

**Consequences:**
- The cache window retained per Viewer is `[pos − lag, pos + lead]`: a deliberate lag keeps recently
  played Segments alive in peers so stragglers and shallow mid-joiners are served by the Swarm; only
  a deep/cold join cold-pulls its initial window from the Host before rejoining the group window.
- A relaying Viewer must serve peers concurrently with its own playback, gossip, and sync, so Segment
  serving must move off the inline control read loop to worker goroutines (the deferred multi-stream
  concurrency fix is folded into this slice).
