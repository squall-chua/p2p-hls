# Watch Party Danmaku: ephemeral reactions, Host-relayed over the control star

**Status:** accepted

A [Watch Party](../../CONTEXT.md) has hard-synced playback and an Audience but no way
for participants to react to one another mid-playback. We add **Danmaku**: any
participant types a short text and it scrolls right-to-left over the video for the Host
and the whole Audience. Two decisions define its shape; they are interdependent and
recorded together. (Full design: [`docs/superpowers/specs/2026-06-10-watch-party-danmaku-design.md`](../superpowers/specs/2026-06-10-watch-party-danmaku-design.md).)

## Decision 1 — Danmaku is ephemeral and live, not timeline-anchored

A Danmaku exists only as it flies across. It is not stored, carries no playback
position, and is never replayed: seeks, late joiners, and re-watches do not bring it
back. Its on-screen motion is wall-clock, so it keeps scrolling while the video is
paused, scrubbing, or buffering.

**Why:**
- Playback is already hard-synced (ADR 0004), so every Viewer present is at the same
  position when a Danmaku is sent. A live overlay therefore already shows everyone the
  same reaction at the same playback moment — the core value — with no anchoring
  machinery.
- "Live, not anchored" is the deliberate boundary: a Danmaku means "someone said this
  *now*," not "this belongs to timestamp T." Anchoring it to the timeline would imply a
  relationship we do not want and would diverge a buffering Viewer's screen from the
  group's.

**Rejected alternative:** Bilibili-style timeline-anchored danmaku (each comment pinned
to a playback position, replayed whenever anyone reaches it). It is the literal meaning
of "danmaku", but it requires a per-Title comment store on the Host, seek-replay logic,
and late-joiner backfill in `PartyWelcome` — a much larger feature whose only gain over
the live model (replay on seek / for late joiners) we do not need for a first cut.

**Consequences:**
- The Host stores nothing; teardown is trivial (overlay clears, nothing persists).
- The wire message carries no position and no sequence number — best-effort,
  fire-and-forget. A dropped Danmaku is simply lost, which is acceptable for ephemeral
  reactions and avoids any retransmit/dedup bookkeeping.
- Promoting to anchored later is an additive protocol change (a position field + a
  store), not a silent tweak — hence recording the current choice here.

## Decision 2 — Danmaku is relayed through the Host (control-plane star); the Host is the single ordering point and the authority

A Viewer sends its Danmaku only to the Host (a new `PartyDanmaku` message in the peer
`Envelope` oneof, on the control channel). The Host validates the sender, stamps the
true sender identity, enforces the length and rate caps, then fans the Danmaku out to
every Audience member — exactly mirroring how `PartyState` and `PartyAudience` already
broadcast. A sender renders its own Danmaku on the Host's echo, not optimistically.

**Why:**
- The Host already is the party hub: it tracks Audience membership and fans out all
  other party control messages. Reusing that path is one code path and needs no new
  membership or delivery model.
- A single broadcaster is a single ordering point, so every participant — including the
  original sender — sees the same set in the same order with zero duplicate-render
  bookkeeping. The extra Viewer→Host→Viewer hop (~hundreds of ms) is invisible for chat.
- The Host being the sole authority is what makes the caps enforceable: it stamps
  sender identity (anti-spoof, Viewers cannot forge), drops Danmaku from a non-Audience
  remote, rune-caps the text, and rate-limits per sender. A hacked client cannot bypass
  limits enforced at the broadcaster.

**Rejected alternative:** distribute Danmaku over the **Swarm** (ADR 0005) — the
Viewers already form a Viewer↔Viewer gossip mesh. Rejected because the Swarm exists to
re-serve *Segment* bytes (with BLAKE3 integrity and have-map gossip), not to carry a
reliable, ordered, low-volume control broadcast; danmaku over it would need its own
N×N delivery and ordering logic for a latency win that does not matter to chat, while
losing the single authoritative choke point that makes anti-spoof and flood control
simple.

**Consequences:**
- Anti-flood is layered with the Host token bucket as the authoritative layer; an
  invariant ties the client send-cooldown to the bucket refill so an honest client
  never has its own Danmaku dropped (see the spec). The Host bucket then only bites a
  misbehaving client.
- The `/party` loopback socket gains a second, asynchronous writer (the inbound-Danmaku
  push alongside the viewer Action-ticker); all writes for a connection are serialized
  through one writer goroutine, since concurrent gorilla-websocket writes are unsafe.
- Sender display names ride along Host-stamped but are not shown yet (anonymous
  overlay); names are a known project-wide gap and no name resolution is pulled in.
