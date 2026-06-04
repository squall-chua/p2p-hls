# Watch-party sync: host-authoritative state-based protocol + RTT/2 clock

**Status:** accepted

A Watch Party keeps Viewers' playback synced to a Host. Two decisions define how.

## Decision 1 — Host is the sole clock authority; sync is communicated as authoritative *state*, not as discrete events

The Host's playback is the single source of truth. It broadcasts a `PartyState` — the full playback state `{playing, position_ms, rate, seq}` — on a periodic heartbeat **and** immediately on any change. Viewers apply the highest-`seq` state they have seen and reconcile their local player toward it (gentle `playbackRate` nudge within a deadband, hard seek beyond a threshold). There are no separate Play/Pause/Seek event messages.

**Why:**
- The WebRTC data channel can drop or reorder messages. Under an event model, a lost "pause" desyncs a Viewer indefinitely. Under a state model, a lost `PartyState` self-heals on the next heartbeat — latest state wins.
- A late-joining Viewer needs the current state to start anyway. The same message that bootstraps a joiner keeps everyone synced: one mechanism, not two.
- Playback is fully describable as state `{playing, position, rate}` — there are no one-shot actions that state cannot represent. `seq` imposes a total order so stale states are ignored.

**Rejected alternative:** discrete `Play`/`Pause`/`Seek` event messages. More granular and intuitive, but fragile to loss/reordering, requires a separate snapshot path for late joiners, and offers nothing state-based lacks for pure playback control.

**Consequences:**
- Rapid Host scrubbing would emit a burst of seek-states. The Host **debounces** seek emission (~150–200 ms settle) and may mark itself buffering mid-scrub so Viewers hold rather than chase intermediate positions.
- A Watch Party is identified by `(Host, party_id)`; `content_id` is only the join reference. Combined with `seq`, an ended or replaced party is unambiguous.

## Decision 2 — Viewers estimate the Host's live position with RTT/2 anchored on receipt; no absolute clock synchronization

A Viewer does not synchronize clocks with the Host. It estimates one-way delay `owd ≈ RTT/2` (from the existing `Ping`/`Pong`, capped to reject spikes); on receiving a `PartyState` it records its **own** monotonic `recvAt`; and computes the expected Host position now as:

```
H = playing ? (position_ms + owd + (now − recvAt)) : position_ms
```

(When the Host is paused it is not advancing, so `owd` is irrelevant and the expected position is exactly the reported `position_ms`.)

**Why:**
- It avoids NTP-style offset machinery entirely. Only RTT is needed, which `Ping`/`Pong` already provides.
- For the ~250–500 ms sync target over typical home-network latencies (RTT in the tens of ms), even large path asymmetry contributes only tens of ms of error — within the nudge deadband.

**Rejected alternative:** NTP-style offset estimation (echo the Host monotonic clock in `Pong`, compute clock offset + delay). More accurate under asymmetric latency, but more moving parts and unnecessary for the target. `PartyState.host_clock_ms` is carried on the wire reserved for this fallback if integration tests show RTT/2 too coarse; it is otherwise unused.

**Consequences:**
- Sync accuracy degrades under highly asymmetric network paths; the documented escape hatch is the NTP fallback (`host_clock_ms` is already on the wire).
- The Viewer correction loop (rate-nudge within deadband, seek beyond threshold) lives in Go and is tested against a virtual clock — no browser needed in CI.
