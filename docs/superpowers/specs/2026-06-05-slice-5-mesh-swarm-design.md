# Design Spec — Slice 5: Mesh Swarm Segment Distribution

**Date:** 2026-06-05
**Status:** approved (brainstorm + grill-with-docs complete)
**Related:** ADR 0005 (mesh swarm distribution), ADR 0004 (watch-party sync), CONTEXT.md (Swarm, Segment)

## Summary

In a Watch Party every Viewer currently pulls every Segment 1:1 from the Host, so the Host's upload
scales with the Audience. This slice introduces the **Swarm**: the Viewers of one party re-serve
cached Segments to one another over a gossip mesh, so the Host originates each Segment roughly once
and the Swarm propagates it. Playback control, the bridge, and hls.js are untouched — the change
lives entirely behind the existing `bridge.Streamer` pull seam that ADR 0004 was built against.

## Key Decisions (see ADR 0005 for rationale)

1. **Party-scoped.** Sharing is confined to one Host's Audience (same transcode → identical Segment
   bytes). No cross-host / title-wide swarm.
2. **Host-anchored integrity.** The Host BLAKE3-hashes each Segment as ffmpeg produces it and injects
   `#EXT-X-P2P-HASH` per Segment into the playlist it serves. The playlist (and hashes) is fetched
   **only from the Host** — the single trust root; peer-supplied bytes are verified against it.
3. **Gossip data plane.** Compact have-maps spread epidemically (periodic, randomized, partial-view,
   anti-entropy); Segment bytes are pulled on demand. Chosen for decentralization; parties stay small.
4. **Decentralized connection plane.** Viewer↔Viewer dial uses lower-`NodeID`-dials to resolve glare;
   the "dial-me" nudge rides a new opaque **signaling-relay** payload — the Host is in neither the
   data nor the connection plane.
5. **RTT-aware, Host-last.** Pull from the lowest-RTT capable peer (reusing the ADR-0004 `Ping`/`Pong`
   RTT); the **Host is the last-resort source** — that deprioritization is what produces the offload.
   No incentives (Host fallback guarantees liveness).

Terminology (CONTEXT.md): the Host is the Segment **origin** — never "seed/seeder". Viewers **relay**.

## The Seam (core boundary)

`hls.js` → loopback bridge `/s/{token}/{node}/{cid}/{name}` → `Node.Segment(ctx, host, contentID, name)`.

Today `Node.Segment` is a straight `session(host).GetSegment`. This slice makes it **swarm-aware**:
if `(host, contentID)` is an active party this Node is *viewing*, it delegates to the swarm coordinator
(peer-pull → verify → cache → re-serve, with Host fallback); otherwise it keeps today's direct-Host
pull. **The bridge, hls.js, and the party sync control plane do not change.**

## Architecture & Components

### New package: `swarm` (the pure engine)
No I/O. Inputs: local have-set, peers' have-maps, per-peer RTT, current playback window, injected
`Clock` and RNG. Outputs are *decisions*: which peers to gossip to this tick, which peer to pull a
Segment from (or fall back to Host), which peers to demote. This is the deterministically-tested core,
mirroring the slice-4 `party` engine. Network effects are behind interfaces the coordinator supplies.

### `app` swarm coordinator (the I/O shell, beside `partyCoordinator`)
Owns the live party's swarm instance. Bootstraps the peer set from the Host's `PartyAudience`
broadcast; performs real dials with glare resolution; sends `SwarmHave` gossip; pulls Segment bytes
over the bulk channel; runs BLAKE3 verification; manages the swarm Segment cache; and answers inbound
`GetSwarmSegment` requests (off the control read loop — see concurrency below).

### Swarm Segment cache
Bounded, windowed cache of verified `(party, segName) → bytes`, window `[pos − lag, pos + lead]`.
Feeds the have-map (have = "in-window, cached, verified"); serves peers; evicts outside the window.
Reuses the windowing instinct of `internal/media/cache.go` but keyed per party + Segment.

### Host-side hash injection (`internal/media`)
As ffmpeg writes each `.ts`, compute its BLAKE3 and record it; when serving `index.m3u8`, inject an
`#EXT-X-P2P-HASH:<b3>` line before each Segment URI. Incremental as the playlist grows.

### Glare resolution (`internal/app` + `internal/signaling`/`internal/peer`)
Lower-`NodeID` dials; higher waits. A higher-id node that wants an edge sends a new opaque
signaling-relay payload ("dial-me") to the lower-id peer, which then dials.

### Concurrency fix (folded in — the deferred `TODO(slice-4)` at `session.go:632`)
A relaying Viewer must serve peers while playing, gossiping, and syncing. Segment serving moves off
the inline control read loop onto worker goroutines so one slow upload can't head-of-line-block a
Viewer's gossip or party sync.

### Unchanged
`bridge`, hls.js, the party sync engine + `PartyState` plane, download/whole-file integrity.

## Wire Protocol

Protobuf `Envelope` additions (run `make proto`, then grep generated names — protoc drops the trailing
underscore except the pre-existing `Envelope_Playlist_`):

- **`SwarmHave { party_id, base_index, bitmap, epoch }`** — control-channel gossip on viewer↔viewer
  sessions. `base_index` + `bitmap` express the windowed have-set compactly; `epoch` is monotonic so
  receivers ignore stale maps.
- **`GetSwarmSegment { party_id, rendition, seg_name }`** — request; bytes return over the **bulk**
  channel (reuses existing bulk framing). Errors: `ErrBusy` (uploader at cap), `ErrNotFound` (have-map
  lie / evicted).

Signaling layer: a new opaque relay payload kind multiplexed alongside the existing SDP/ICE signal,
carrying the glare "dial-me" nudge `{ party_id, from }`.

Playlist: the `#EXT-X-P2P-HASH` custom tag (hls.js ignores unknown tags).

## Swarm Engine (the Go core)

**Have-map.** Per party, the windowed set of Segments held, as `base_index + bitmap`. A Viewer joins
the Swarm the instant it is in the Audience, starting with an empty have-map and contributing as it
caches.

**Gossip loop (push-pull, per `Clock` tick ≈ 1s).** Pick `f` targets — RTT-biased **plus ≥1
uniformly-random** (random link preserves mixing) — from the active peer set; push `SwarmHave`; the
receiver merges and pushes its own back. Peer-have entries carry a TTL so a silent peer decays out;
`SetOnClose` (`node.go:124`) removes a disconnected peer immediately.

**Fetch (pull, on demand).** For Segment K: cached → serve. Else candidates = peers advertising K and
not known-busy, ranked by RTT; pick lowest → `GetSwarmSegment` → bulk bytes → **BLAKE3-verify vs Host
hash** → pass: cache + serve + flip have-bit (propagates next round); fail/`ErrBusy`/timeout: try next,
then Host. No peer has K → Host directly.

**Upload cap.** Concurrent outbound relays are bounded by a semaphore; excess → `ErrBusy`.

**Default constants (named, tunable later).** gossip tick, fanout `f`, random-link count, peer-have
TTL, window `lag`/`lead`, upload cap, pull timeout.

**Virtual clock + seeded RNG for tests.** The engine takes both as inputs; CI exercises gossip
convergence and selection deterministically, no browser, no real network.

## Integrity & Trust

The Host-served playlist is the only trusted artifact. Every peer-supplied Segment is BLAKE3-verified
against the Host hash before caching/serving/re-serving. A verify-fail or `ErrNotFound` **demotes** the
peer (drop its have-map, stop selecting it this party) and the fetch retries the next candidate, then
the Host. WebRTC (SCTP/DTLS) covers wire corruption; this defends against a dishonest *sender*.

## End-to-End Flows

- **Steady state:** Host originates Segment K once; the first puller verifies + caches + advertises it;
  peers pull K from each other thereafter. Host upload per Segment ≪ Audience size.
- **Shallow straggler (1 buffer behind):** needs a recent Segment still in peers' lag window → served
  by the Swarm.
- **Deep mid-join (joins 10 min in):** cold-pulls its initial window from the Host, then rejoins the
  group window where the Swarm takes over.
- **Poison / lie:** verify-fail or `ErrNotFound` → demote → next candidate → Host. Playback uninterrupted.
- **No reachable peers (symmetric NAT, party of 1):** degrades to today's direct-Host pull.

## Error Handling & Edge Cases

- Unproduced Segment at the buffer edge: Host returns `ErrUnavailable` (retry), unchanged from today.
- Host seek: the window shifts; out-of-window cache evicts; the cold region is Host-served then
  re-propagates (rarest = newest).
- Peer disconnect mid-transfer: pull times out → next candidate / Host; peer removed from view.
- Stale have-map (`epoch`): ignored.

## Assumptions (confirmed during brainstorm + grill)

- Parties are friend-sized (≤ ~16); gossip chosen for decentralization, not scale.
- Within a party, Segment bytes are identical across Viewers (one Host transcode).
- Every Node already holds multiple peer sessions; viewer↔viewer dialing is mechanically supported.
- Single rendition today; have-map keyed by `seg_name`, the rendition dimension a noted future seam (ABR).

## Testing Strategy

- **`swarm` engine unit:** virtual `Clock` + seeded RNG + injected RTT/have-maps — gossip convergence,
  source selection (lowest-RTT capable, skip busy, fallback order), peer demotion.
- **Integrity unit:** BLAKE3 accept/reject; poison → Host fallback.
- **Playlist injection unit (`media`):** correct `#EXT-X-P2P-HASH`; existing playlist tests stay green.
- **Glare unit:** deterministic initiator by `NodeID` ordering.
- **e2e (real sessions, like the slice-4 party e2e):** party of N — (a) all Viewers get all Segments
  verified; (b) **Host serves each Segment ≪ N times** (offload proof); (c) poisoning Viewer routed
  around; (d) mid-stream joiner catches up via the Swarm.
- `go test -race ./...` stays green; engine tests deterministic (no flakes).

## Success Criteria

1. Party of N: Host upload per Segment ≪ N, all Viewers play verified Segments. *(headline)*
2. Playback byte-identical to today; bridge/hls.js/sync untouched.
3. A malicious peer cannot corrupt playback.
4. `go test -race ./...` green; swarm engine tests deterministic.
5. A Viewer with no reachable peers degrades to direct-Host pull and still plays.

## Out of Scope (this slice)

- Cross-host / title-wide swarm (Segments not content-addressed across Hosts).
- Incentives / tit-for-tat beyond the upload cap.
- TURN for viewer↔viewer (symmetric-NAT peers fall back to the Host; TURN is the separate NAT slice).
- ABR multi-rendition (single rendition today; rendition is a noted have-map seam).
- Browser/actuator changes (none — seam unchanged); persisting the swarm cache across restarts.
