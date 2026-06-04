# Design Spec — Slice 4: Watch-Party Synced Playback

- **Date:** 2026-06-04
- **Status:** Approved (brainstorm); to be hardened via `grill-with-docs` before planning.
- **Builds on:** Slices 1–3 (`docs/superpowers/specs/2026-06-03-p2p-hls-design.md`), merged at `70e872e`.
- **Scope of this spec:** The **synced-playback** half of the watch-party vision only. Mesh swarm load-sharing is a separate fast-follow (**Slice 5**).

## Summary

A **host** runs a watch party on one title. Access-allowed **guests** join — either by spotting a live party while browsing (advertised) or by host invite — and their players stay **hard-synced** to the host's playback position (~250–500ms steady-state) across play, pause, seek, and late-join catch-up. Only the host controls playback; guests are followers.

Guests still pull segments **1:1 from the host** over the existing streaming path — this slice adds *coordination of when everyone plays*, not a new way to move bytes. Multi-source segment distribution (the swarm) is Slice 5 and slots in under the unchanged `httpbridge`↔`peer` seam later.

## Key Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Subsystem split | **Sync first (this slice), swarm later (Slice 5)** | Smaller, visible, demoable; de-risks the big piece; gives the swarm a concrete workload to optimise against. |
| Sync engine home | **Go core + thin JS actuator** | The state machine, clock math, and correction decisions live in Go and are deterministically testable against a virtual clock — matching the slices 1–3 discipline. The browser player is a dumb actuator. |
| Control authority | **Host-only** | Single source of truth for playback; no conflict resolution. Co-host/delegated control deferred. |
| Join model | **Both advertised + invite** | Advertised join matches the existing viewer→host direction and access flow; invite supports proactive "let's watch now." The sync engine is identical regardless of how a guest arrived. |
| Drift correction | **Hybrid rate-nudge + seek** | Small drift → gentle `playbackRate` nudge (smooth, imperceptible); large drift or explicit seek → hard seek. Production-standard. |
| Clock model | **RTT/2 anchored on receipt** (no NTP) | Guest anchors on its own receive time and adds one-way delay; only RTT is needed, no absolute clock sync. Fallback (echo host clock in `Pong`) noted, not built. |
| Topology | **Star (host ↔ each guest)** | No guest-to-guest; reuses the existing per-guest authenticated control channel. Mesh is Slice 5. |
| Actuator wire | **Loopback WebSocket** | The Go core must *push* commands and the player must *report* position; WS gives instant bidirectional delivery with no poll lag. |
| Party discovery | **Annotate `TitleMeta`** (`party_live`, `party_viewers`) | Minimal new surface; party already keys off `contentId`. A dedicated `ListParties` RPC was the rejected alternative. |

## The Two Wires (core boundary)

- **Intra-node loopback WebSocket** — between a node's *own* browser player and that node's *own* Go sync engine. Player reports `currentTime`/state up; engine pushes `play`/`pause`/`seek`/`setRate` commands down. Present on **both** host and guest nodes (each node's player is hls.js pointed at its own loopback bridge; the only difference is who owns the clock).
- **Inter-node peer control channel** — the *existing* authenticated WebRTC data channel between host and guest nodes, carrying party messages (heartbeats, control state, join/leave, roster). **No guest-to-guest.**

One line: the loopback WS is *intra-node* (player ↔ engine); the peer control channel is *inter-node* (host engine ↔ guest engine). The sync engine is the only new "brain," and it lives in Go where it is deterministically testable.

## Architecture & Components

### New package: `party`
One job: the sync state machine + clock math, pure and virtual-clock-testable. Depends on `peer` (send/receive party Envelopes); consumed by `bridge` (loopback WS) and `app` (wiring). Two roles:

**`party.Host`** — owns authoritative party state:
- `{partyId, contentId, roster, playback}` where `playback = {playing bool, positionMs int64, sampledAtHostClock int64, rate float64, seq uint64}`.
- Receives its local player's `currentTime`/state from the host's bridge WS; **interpolates** position between samples (while playing, `pos + elapsed`) so heartbeats are accurate between reports.
- Emits `PartyState` to all guest sessions: periodically (heartbeat, ~500ms) **and immediately on any change** (play/pause/seek bumps `seq`).
- Handles `JoinParty` (admit subject to access policy), `LeaveParty`, roster broadcast, and `PartyEnded` on stop.

**`party.Guest`** — receives host state, drives its local player:
- Estimates one-way delay from RTT (Ping/Pong), extrapolates expected host position, runs the drift-correction state machine, emits `seek`/`setRate`/`play`/`pause` commands to its local player via the bridge WS.
- Reports its player's `currentTime` up (to compute drift).
- Rejects out-of-order `PartyState` via `seq`.

### `bridge` additions
A `/party` WebSocket endpoint on the existing loopback server (Origin-checked + token-gated like today's `/s/`). It is the local adapter that marshals player↔engine messages. The bridge stays a dumb conduit — **no sync logic lives here**.

### `app` wiring
Constructs `party.Host`/`party.Guest` alongside the existing session, routes new party Envelopes from `peer` into the engine, and exposes "start/stop party" and "join party" entry points.

### Unchanged
`media` (segment production), `peer` framing/`bulk` channel, `catalog` access control, and the 1:1 `GetPlaylist`/`GetSegment` streaming path.

## Wire Protocol (protobuf `Envelope` additions)

Existing oneof fields 2–16 are used; next free is **17**. Existing `Ping`/`Pong` are reused for RTT. New messages:

```proto
JoinParty    join_party     = 17;  // guest→host: {string content_id}
PartyWelcome party_welcome  = 18;  // host→guest: {string party_id, PartyState initial, PartyRoster roster}  | else Error(DENIED)
PartyInvite  party_invite   = 19;  // host→guest: {string content_id, string party_id, string host_display}
PartyState   party_state    = 20;  // host→guests: {string party_id, bool playing, int64 position_ms,
                                   //               int64 host_clock_ms, double rate, uint64 seq}
                                   //   host_clock_ms = host monotonic clock at sample time; informational,
                                   //   reserved for the NTP fallback. The default RTT/2 model does not use it.
PartyRoster  party_roster   = 21;  // host→guests: {string party_id, repeated Participant members}
LeaveParty   leave_party    = 22;  // guest→host: {string party_id}
PartyEnded   party_ended    = 23;  // host→guests: {string party_id, string reason}
```

- `PartyState` is the single source of truth: heartbeat and control-event are the **same** message. A play/pause/seek changes the fields and bumps `seq` for an immediate send. Guests apply the highest-`seq` state they have seen and reject lower-`seq` (stale/out-of-order) states.
- `Participant` = `{string node_id, string display_name}`.
- **Capability negotiation:** host advertises `"party/v1"` in `Handshake.capabilities`; a guest without it cannot join (graceful refusal).
- **Advertised discovery:** add `bool party_live = 12` and `int32 party_viewers = 13` to the existing `TitleMeta` (fields 1–11 are in use). A browsing guest sees the live flag directly.
- Verify generated Go names against `peer.pb.go` after `make proto` (protoc disambiguation bit slices 1–3, e.g. `Envelope_Playlist_`).

## Sync Algorithm (the Go core)

### Clock model — no NTP offset
The guest anchors on its *own* receive time:
- Measures `RTT` via periodic Ping/Pong; one-way delay `owd ≈ RTT/2` (capped to reject spikes).
- On receiving a `PartyState`, records `recvAt` (guest monotonic clock) plus the state.
- Expected host position **now**: `H = position_ms + owd + (playing ? (now − recvAt) : 0)`.

This avoids absolute clock synchronisation entirely — only RTT matters. If integration tests show RTT/2 is too coarse for the target band, the documented fallback is to echo the host clock in `Pong` for true NTP-style offset estimation (noted, not built in this slice).

### Correction state machine
Runs each time the guest reports `currentTime` (~250ms cadence):
1. `drift = guestCurrentTime − H` (positive ⇒ guest ahead).
2. **State mismatch** (host paused & guest playing, or vice-versa) ⇒ apply `play`/`pause` to match host.
3. **`|drift| > SEEK_THRESHOLD` (≈1000ms) or a new host SEEK `seq`** ⇒ command `seek(H)`, `rate = 1.0`.
4. **`NUDGE_DEADBAND` (≈80ms) < `|drift|` ≤ SEEK_THRESHOLD** ⇒ `rate = clamp(1 − k·drift, 0.92, 1.08)` (ahead → slow down, behind → speed up).
5. **`|drift|` ≤ NUDGE_DEADBAND** ⇒ `rate = 1.0`.

### Default constants (named, tunable later)
| Constant | Default |
|---|---|
| `SEEK_THRESHOLD` | ~1000 ms |
| `NUDGE_DEADBAND` | ~80 ms |
| Rate clamp | 0.92×–1.08× |
| Heartbeat interval (host) | ~500 ms |
| Guest report cadence | ~250 ms |

### Virtual clock for tests
The engine takes a `Clock` interface (`Now()`) and the player position as an *input*, returning *commands* as output — no real time, no browser. Tests script host `PartyState` sequences plus a synthetic guest player (position advances by `rate·dt`) and assert convergence into the target band.

## End-to-End Flows

1. **Open party (host):** host clicks "watch party" on a local title → `party.Host{partyId, contentId}` created, local player starts via bridge WS, `TitleMeta.party_live = true`, heartbeats begin (empty roster).
2. **Advertised join:** guest browsing sees `party_live` → clicks join → (connect/reuse session) → `JoinParty{contentId}` → host access-check → `PartyWelcome{initial PartyState, roster}` → guest starts the existing 1:1 stream → player loads, seeks to host position, steady-state sync engages.
3. **Invite join:** host sends `PartyInvite` to a chosen online node → guest accepts → `JoinParty` → same as above.
4. **Play/pause/seek:** host player event → host engine updates `playback`, bumps `seq`, immediately broadcasts `PartyState` → each guest applies.
5. **Late-joiner catch-up:** initial `PartyState` → seek to host position. Required segments always exist because guests never run ahead of the host (the host is the producer).
6. **Lagging guest:** stalls/buffers → drift exceeds `SEEK_THRESHOLD` → seek to live. The host never waited.
7. **Leave / host-ends:** `LeaveParty` removes from roster; `PartyEnded` drops guests to solo playback (player keeps running).

## Error Handling & Edge Cases

- **Guest peer session drops** → auto-removed from roster on disconnect; guest UI shows "disconnected," may reconnect + rejoin (extends existing peer disconnect handling).
- **RTT spikes** → `owd` capped; if RTT is unstable, prefer seek over rate-nudge.
- **Out-of-order / stale `PartyState`** → rejected by `seq`.
- **Access revoked mid-party** → next `PartyState`/segment denied; guest dropped from the party.
- **Host hits transcode "not ready" (live edge)** → host position stalls → heartbeats reflect the stall → guests naturally wait. No guest can outrun the host (it is the producer).
- **Missing `party/v1` capability** → join refused gracefully.

## Assumptions (confirmed during brainstorm)

1. **Host never waits.** A guest that can't keep up falls behind, buffers, and re-syncs by seeking when it has the data. Host playback is never blocked by a slow guest.
2. **One party per host at a time** (one `contentId`). Multiple concurrent parties per host is deferred.
3. **Access control reused as-is.** Joining a party = streaming that title; the existing per-node allow/block policy gates both invite and advertised joins. Roster node IDs are trustworthy (they ride the already-authenticated channel).
4. **Lightweight roster.** Host keeps the authoritative roster and broadcasts a participant list/count ("N watching"); guests don't track each other's positions.
5. **On host-ends / guest-leave,** a guest drops out of sync but keeps its player as a normal solo stream (no hard stop).

## Testing Strategy

Mirrors the slices 1–3 discipline (TDD; `go test -race ./...` stays green):

- **Unit — `party` engine, virtual clock:** deterministic convergence into the target band; play/pause/seek propagation; late-join seek; lag→seek recovery; out-of-order `seq` rejection; `owd`/half-RTT extrapolation math.
- **Integration — real `peer` sessions, no browser:** two in-process nodes over actual data channels; host engine + a **Go fake actuator** for the guest (position advances by `rate·dt`); assert `PartyState`/roster/join/leave delivery and end-to-end convergence. Reuses the existing e2e harness style.
- **Not automated:** real `<video>`/hls.js sync (manual demo). The JS actuator is thin; its contract (report `currentTime`, apply commands) is small and exercised by hand.

## New ADR (to be written during `grill-with-docs`)

`docs/adr/0004-watch-party-sync-protocol.md` capturing: the clock model (RTT/2 anchored-on-receipt vs NTP), host-as-authority, and the single-`PartyState`-message design.

## Out of Scope (this slice)

Mesh swarm load-sharing (Slice 5); co-host / delegated control; multiple simultaneous parties per host; chat / reactions; the full Vue watch-party UI (only the thin JS actuator); TURN / trickle ICE; ABR / multi-rendition.
