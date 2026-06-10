# Watch Party Danmaku — Design

Date: 2026-06-10

## Problem

A [Watch Party](../../../CONTEXT.md) currently has hard-synced playback and an
[Audience](../../../CONTEXT.md), but no way for participants to react to each other
during playback. We want lightweight live reactions: any participant types a short
piece of text and it appears as floating text scrolling **right-to-left** over the
video for the whole Audience — classic *danmaku*.

## Concept / new term

**Danmaku** — a short, ephemeral text reaction a participant posts during a Watch
Party, rendered as floating text scrolling right-to-left over the video for the
whole Audience. It is **not stored** and **not tied to a playback position**: it
exists only as it flies across, live. _Avoid: chat, message, comment._

This term will be added to [CONTEXT.md](../../../CONTEXT.md)'s Watch Party glossary
during the grilling pass.

## Decisions (confirmed with user)

- **Ephemeral, not timeline-anchored.** A Danmaku flies across once and is gone.
  Nothing is stored; seeks and late joiners do **not** replay it. (Rejected the
  Bilibili-style anchored model — given hard-synced playback, everyone present
  already sees a live Danmaku at the same playback moment, so anchoring's only real
  benefit, replay for seeks/late-joiners, isn't worth the comment store + seek-replay
  + backfill machinery for a first cut.)
- **Everyone in the party posts.** Host *and* every Viewer can post. The overlay
  renders **anonymously** (floating text only, no name shown). Sender identity is
  still carried on the wire (Host-stamped, anti-spoof) so a future "who said that"
  toggle is cheap, but nothing displays it now.
- **Transport: route through the Host (star).** A new `PartyDanmaku` message in the
  peer `Envelope` oneof. A Viewer sends its Danmaku to the Host; the Host fans it
  out to every Audience member. This mirrors how `PartyState` / `PartyAudience`
  already broadcast ([`party.go`](../../../internal/app/party.go) `broadcastAudience`,
  `heartbeat`). (Rejected a Swarm mesh broadcast — the Swarm is built for *Segment*
  distribution, not a guaranteed control mesh; the Host is already the hub and
  already tracks membership.)
- **Host is the single ordering point.** A Viewer only ever sends a Danmaku to the
  Host — never peer-to-peer. The Host fans out to *all* members (including the
  original sender), so everyone sees the same set in the same order. The sender
  renders its own Danmaku **on receipt** (~one round-trip, invisible for chat), not
  optimistically — one consistent global order, zero duplicate-render bookkeeping.
  The Host, being the ordering point, renders its own immediately on send.
- **Input placement:** a **slim, bottom-center input pill** revealed by the existing
  reveal-on-hover chrome (hover/focus), not always-on. Hidden in `solo` role.
- **Flood control and a length cap are authoritative on the Host** (below). The
  scroll speed stays constant and readable; flooding is bounded by rate + density,
  not by speeding up the scroll.

## Data flow

```
Viewer types "lol" ──ws──▶ Viewer Node ──PartyDanmaku(text)──▶ Host Node
                                                              │
                              Host: validate sender in        │
                              Audience, cap/trim text,        │
                              rate-bucket, stamp identity,    │
                              then fan out                    ▼
        ┌──────────── PartyDanmaku(text, sender) broadcast to EVERY Audience member ───────────┐
        ▼                              ▼                              ▼                       ▼
   Host's own browser           Viewer A browser              Viewer B browser        originating Viewer
   (renders overlay)            (renders overlay)             (renders overlay)        (renders overlay)
```

- **Host originates:** the browser sends `{type:"danmaku", text}` on the loopback
  `/party` WS → the coordinator broadcasts to all members and pushes to its own
  browser.
- **Viewer originates:** the browser sends `{type:"danmaku", text}` → the
  coordinator sends one `PartyDanmaku{party_id, text}` to the Host. **No** local render.
- **Host receives** a Viewer's `PartyDanmaku`: validate + broadcast to all members +
  push to own browser.
- **Viewer receives** the Host's `PartyDanmaku`: push to own browser.
- **Solo** role has no party WS → no Danmaku (input hidden).

## Part A — Proto

Add to the `Envelope` oneof in
[`proto/peer/v1/peer.proto`](../../../proto/peer/v1/peer.proto) (next free tag = 26):

```proto
PartyDanmaku party_danmaku = 26;

// PartyDanmaku carries one Danmaku. A Viewer→Host send fills only `text`; the Host
// overwrites sender_* with the true remote identity before fanning out.
message PartyDanmaku {
  string party_id       = 1;
  string sender_node_id = 2; // set by the Host on fan-out; ignored on inbound
  string sender_display = 3; // set by the Host on fan-out
  string text           = 4; // sender-provided, length-capped (runes) on the Host
}
```

Regenerate with `make proto` (`protoc --go_out`). It is **fire-and-forget** over the
control channel — reuses [`Session.SendControl`](../../../internal/peer/party.go#L41);
no new request/response RPC, no retries. Kept under the existing `party/v1` capability
([`CapParty`](../../../internal/peer/party.go#L12)) — a party peer that predates the
feature simply has no `case` for the new oneof and ignores it.

## Part B — Peer session

- [`peer.PartyHandler`](../../../internal/peer/party.go#L17) gains
  `OnPartyDanmaku(remote identity.NodeID, pc *peerv1.PartyDanmaku)`.
- The session read-loop dispatch in
  [`session.go`](../../../internal/peer/session.go) gains a `case` for
  `*peerv1.Envelope_PartyDanmaku` that calls the handler. (No reply: fire-and-forget.)

## Part C — Node orchestration (`partyCoordinator`)

In [`internal/app/party.go`](../../../internal/app/party.go):

- **`broadcastDanmaku(senderNode identity.NodeID, senderDisplay, text string)`** —
  the single fan-out point. Trims + rune-truncates `text` to `MaxDanmakuLen`, drops
  if empty, builds the `PartyDanmaku` Envelope, `sendTo` every Audience member, and
  pushes to the local browser sink. Called by both the Host-originate path and the
  Host-receive path.
- **`OnPartyDanmaku(remote, pc)` (PartyHandler):**
  - **Host role** (`pc.host != nil`): drop if `remote` is not a current Audience
    member (anti-spoof); otherwise apply the per-sender rate bucket; if admitted,
    `broadcastDanmaku(remote, audienceName(remote), pc.Text)`.
  - **Viewer role** (`remote == viewerHost`): push straight to the local browser
    sink. Ignore `PartyDanmaku` from anyone other than the current `viewerHost`.
- **Sink registration.** At most one watch page is open, so the coordinator holds a
  single active-connection sink. `serveWS` registers it on connect, clears it on
  disconnect.
- **Single writer per WS connection (safety fix).** Danmaku adds a *second,
  asynchronous* writer to the `/party` socket (the viewer Action-ticker is the
  existing one; the host loop never wrote before). Concurrent gorilla-websocket
  writes are unsafe, so all writes for a connection funnel through **one writer
  goroutine + a buffered channel** (`out chan []byte`, drop-on-full). The viewer
  Action-ticker and the Danmaku sink both send to `out`.
- **WS read handling** ([`serveHostWS`](../../../internal/app/party.go#L473) /
  [`serveViewerWS`](../../../internal/app/party.go#L503)): branch on `m.Type ==
  "danmaku"`. Host → `broadcastDanmaku(self, selfDisplay, m.Text)`. Viewer → `sendTo`
  the Host a `PartyDanmaku{party_id, text}` (no local render). `playerMsg` gains a
  `Text string json:"text"` field.

Sender display uses whatever the Audience already stores (today the Node ID string —
display names are a known project-wide gap; we carry the Audience's value and don't
show it yet regardless).

## Part D — Pure limiter + length helper (`internal/party`)

New clock-driven, pure helpers in `internal/party`, mirroring the existing
`Host`/`Viewer` engine style (a `Clock` is the only ambient input, so tests use a
virtual clock):

- **`MaxDanmakuLen = 100`** (runes). A `CapText(s string) string` helper that trims
  whitespace and truncates to `MaxDanmakuLen` **runes** on a rune boundary (danmaku
  is often CJK/emoji; a byte cap would split characters and be unfair).
- **Per-sender token bucket** (e.g. `DanmakuGate`): refill ≈1 token/sec, burst ≈3,
  keyed by sender `NodeID`, advanced by `Clock`. `Allow(sender, now) bool`. Tunables
  live in [`party.Config`](../../../internal/party/party.go#L25) /
  `DefaultConfig()`. Over-budget Danmaku are dropped before broadcast.

## Part E — Loopback WS protocol

Browser ↔ Node over `/party/{token}`
([`party_ws.go`](../../../internal/bridge/party_ws.go), driven from
[`player.ts`](../../../webui/app/lib/player.ts)):

- **Browser → Node** gains `{type:"danmaku", text}` from either role.
- **Node → Browser** gains `{type:"danmaku", text, sender}` pushed on receipt.
- The browser `onmessage` **discriminates on `type`**: `"danmaku"` → overlay; anything
  without that type → the existing viewer `Action`. The **host** gains an `onmessage`
  for the first time, used only to receive Danmaku.

## Part F — Front-end

- **`attachPlayer`** ([`player.ts`](../../../webui/app/lib/player.ts)) gains an
  `onDanmaku?: (d: {text:string; sender?:string}) => void` option and returns a
  `sendDanmaku(text: string)` function on its handle. Both host and viewer roles set
  `ws.onmessage` to dispatch Danmaku to `onDanmaku`.
- **`webui/app/lib/danmaku.ts`** (new, pure, unit-tested) — lane allocation: a fixed
  lane count (≈10) across the top ~60% band; a lane accepts a new item only once its
  current item has scrolled clear; when all lanes are busy, items wait in a small
  **bounded queue (drop-oldest past ≈30)** rather than piling up. Steady ~7 s scroll.
- **`DanmakuOverlay.vue`** (new) — absolutely positioned over the `<video>`,
  `pointer-events:none`. Each item is a `transform: translateX` right→left animation
  over a constant duration; white text + dark text-shadow for legibility on any
  frame; element removed on animation end. Fed by a reactive queue filled from
  `onDanmaku`, placed by `danmaku.ts`.
- **Input** in
  [`watch/[host]/[contentId].vue`](../../../webui/app/pages/watch/%5Bhost%5D/%5BcontentId%5D.vue):
  a slim, bottom-center pill inside the existing reveal-on-hover chrome (hover/focus),
  `maxlength = MaxDanmakuLen`, Enter or Send → `handle.sendDanmaku(text)` then clear,
  with a ≈1/s client send cooldown (matches the Host bucket; gives "slow down"
  feedback instead of silent server drops). Hidden when `role === 'solo'`. The page
  passes `onDanmaku` into `attachPlayer` and pushes items into the overlay queue.

## Flood control (three layers)

1. **Host token bucket per sender (authoritative).** The Host is the single
   broadcaster, so it is the choke point; over-budget Danmaku are dropped, not
   broadcast — protects the entire Audience from any one flooder, unbypassable by a
   hacked client.
2. **Front-end density cap.** Bounded lanes + drop-oldest queue cap on-screen text
   regardless of arrival rate.
3. **Client send cooldown.** ≈1/s, UX-only, immediate feedback.

**Invariant (must hold):** the client cooldown is **≥ the bucket refill rate** and the
bucket **burst ≥ 1**. This guarantees an honest client can never outrun its own bucket,
so the Host always accepts an honest client's Danmaku — which matters because a Viewer
renders its own Danmaku only on the Host's echo (see "Host is the single ordering
point"). Were the cooldown looser than the refill, an honest Viewer's own comment could
be silently dropped and never appear. The bucket therefore only ever bites a
**misbehaving/hacked** client that ignores the cooldown, which is exactly the intent.
The two limits must be tuned together, not independently.

Scroll speed stays constant and readable throughout; flooding is bounded by rate and
density, never by speeding up the scroll.

## Components & responsibilities

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `peer.PartyHandler.OnPartyDanmaku` | deliver inbound Danmaku to the coordinator | — |
| `partyCoordinator.broadcastDanmaku` | cap/rate/stamp + fan out to Audience + local sink | party.CapText, DanmakuGate |
| `partyCoordinator.OnPartyDanmaku` | host validate+broadcast / viewer push-to-browser | broadcastDanmaku |
| WS writer goroutine | serialize all writes on one `/party` conn | — |
| `party.CapText` | trim + rune-truncate to `MaxDanmakuLen` | — |
| `party.DanmakuGate` | per-sender token bucket (Clock-driven) | party.Clock |
| `player.ts` | WS type discrimination; `sendDanmaku`; `onDanmaku` | — |
| `danmaku.ts` | lane allocation + bounded queue | — |
| `DanmakuOverlay.vue` | render scrolling items, cleanup on end | danmaku.ts |
| `watch/[…].vue` input | post Danmaku, client cooldown, reveal-on-hover | attachPlayer |

## Edge cases / known behavior

- **Anti-spoof:** the Host stamps `sender_*` from the true remote identity and drops
  any `PartyDanmaku` from a remote not in the Audience. Viewers ignore `PartyDanmaku` from
  anyone but their current Host.
- **Loss / ordering:** fire-and-forget, best-effort; the Host is the sole ordering
  point; a dropped Danmaku is simply lost (acceptable for ephemeral).
- **Length:** capped on the Host (authoritative) in runes; the client `maxlength` is
  a convenience mirror.
- **Teardown:** party end / Viewer leave / WS close → overlay clears, sink
  unregisters, writer goroutine stops; nothing is persisted.
- **Playback-independent:** Danmaku flow is wall-clock, not tied to `video.currentTime`
  — it keeps scrolling while the video is paused, scrubbing, or buffering (consistent
  with ephemeral/not-anchored; freezing it would imply an anchoring relationship we
  rejected and would diverge a buffering Viewer's screen from everyone else's).
- **Solo:** no party WS → no input, no overlay.
- **One watch page at a time:** the single-sink assumption matches the current UI
  (one theater view). If multiple `/party` connections ever coexist, the sink would
  need to become a set — out of scope now.

## Testing strategy

- **`internal/party`** (new tests): `CapText` trims, rune-truncates on a boundary,
  drops empty, and is byte-safe for CJK/emoji; `DanmakuGate` admits a burst then
  throttles and refills over a virtual clock.
- **`internal/app`** ([`party_test.go`](../../../internal/app/party_test.go) style,
  fake sender): Host `OnPartyDanmaku` fans out to **all** members + local sink; Host
  drops a non-Audience sender; Host drops an over-rate sender; Viewer accepts only
  from its Host; text trim/truncate/empty-drop. WS-loop test: a `{type:"danmaku"}`
  browser message produces the right `PartyDanmaku` (Host → all members; Viewer → Host
  only).
- **webui** (vitest, `webui/test/`): `danmaku.ts` lane allocation, bounded-queue
  drop-oldest, and cleanup; `player.ts` discriminates Danmaku vs Action; input
  cooldown + `maxlength`.
- **Manual** (after `make webui` + `make build`): two Nodes in a Watch Party; post
  from Host and from Viewer; confirm both see each Danmaku scroll once; confirm a
  rapid burst is rate-limited and the screen never floods; confirm length cap.

## Risks / prerequisites

- `make proto` needs `protoc` + `protoc-gen-go` (both present in this environment;
  verify at implementation time).
- UI changes only appear in the served binary after `make webui` rebuilds the
  embedded bundle (the tracked bundle is a placeholder; ADR 0006).
- Display names are Node ID strings project-wide; Danmaku carries that value and does
  not show it yet, so no new name-resolution work is pulled in.

## Out of scope

- Timeline-anchored / replayable danmaku, persistence, late-joiner backfill, seek
  replay.
- Showing the sender's name, per-sender colors, or emotes.
- Danmaku in `solo` playback (no Audience).
- Density/speed user preferences (opacity, lane count, on/off toggle) — fixed
  defaults for now.
- Multiple simultaneous watch pages per Node.
- Real display-name resolution.
