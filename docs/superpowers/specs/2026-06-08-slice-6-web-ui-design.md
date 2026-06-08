# Design Spec — Slice 6: Full Browser UI

**Date:** 2026-06-08
**Status:** approved (brainstorm + grill-with-docs complete)
**Related:** ADR 0006 (browser UI: control-plane API & serving model), ADR 0004 (watch-party sync the player must honour), ADR 0001 (client-agnostic wire protocol; browser-guest future), CONTEXT.md (Node/Peer/Host/Viewer, Library, Catalog, Watch Party, Audience, Swarm)

## Summary

The backend is feature-complete through Slice 5 (identity, signaling, library, browse+access, 1:1 HLS
streaming, verified download, watch-party sync, mesh swarm), but there is **no browser UI**: `cmd/node`
is a CLI that prints a Node ID, and the loopback bridge serves only media (`/s/`) and the player
WebSocket (`/party/`). This slice builds the **full web UI** — a Nuxt 4 single-page app embedded in the
binary, a new HTTP/JSON **control plane** on the bridge, an **SSE event stream** for live updates, and
the long-deferred **real browser actuators** (hls.js player + host/viewer watch-party sync). It closes
the Slice-4 UI leftovers (real actuator, Audience rendering, request/approve UI). No engine logic moves;
this slice is an adapter + presentation layer over the existing `Node` methods.

## Key Decisions (see ADR 0006 for rationale)

1. **Nuxt 4, client-only static, embedded.** `ssr: false` → `nuxt generate` → static `dist/` served by
   the Go bridge via `go:embed`. The single static binary is preserved; the build gains a Node step
   before `go build`. No Node runtime ships (the bridge has no Nitro server). Built with Nuxt UI v4.
2. **REST commands + SSE events.** Control commands are plain JSON request/response handlers (stateless,
   unit-testable). A single `GET /api/events` Server-Sent-Events stream pushes presence / incoming-request
   / audience changes. The player keeps its own `/party/` WebSocket. WebSocket-RPC was rejected as more
   machinery than a loopback control plane needs.
3. **Push via notification hooks.** Presence, incoming-request, and audience changes are delivered to an
   in-process event hub by small change-callbacks added to the signaling client, catalog service, and
   party coordinator — event-driven, no polling.
4. **Host-authoritative player UX.** The host's transport drives all viewers (ADR 0004); the viewer
   transport is **locked** and playback follows the host, with a visible **drift indicator**. This is the
   real browser half of the actuator the Go engine already implements and tests.
5. **Same-origin token bootstrap.** The bridge injects `window.__P2P__ = {token, nodeId, name}` into the
   served `index.html`; the SPA authenticates with the cleanest channel each transport offers — an
   `Authorization` header for `/api/*`, a `?token=` query for `/api/events` (EventSource cannot set headers),
   and the existing path token for `/s/` and `/party/`. Loopback-only binds + the existing Origin check +
   same-origin policy (a cross-origin page cannot read the injected token) keep this safe. Dev falls back to
   reading `?token=` from the URL.

Terminology stays aligned to CONTEXT.md: the UI shows **Peers**, a peer's **Catalog** of **Titles**, the
**Host**/**Viewer** roles, the **Audience** ("N watching"), and the **Watch Party**.

## The Seam (core boundary)

Browser → loopback bridge. Today the bridge exposes exactly `/s/` (media) and `/party/` (player WS). This
slice **adds** three responsibilities to the same loopback server, behind the same loopback + Origin +
token guards:

- `GET /` (+ SPA fallback) → the embedded Nuxt bundle.
- `/api/...` → JSON control handlers that wrap existing `Node` methods.
- `GET /api/events` → SSE stream fed by the event hub.

`/s/` is **unchanged**. The `/party/` role/`hello` protocol is unchanged; the viewer's downstream message
gains **one additive field** (`driftMs`, see Actuators). `Node`'s public methods are unchanged except a
`host == self` short-circuit on `Playlist`/`Segment` (host self-playback, below). The control handlers are a
thin adapter; the only other edits to existing subsystems are the three notification hooks (Decision 3).

## Architecture & Components

### `internal/bridge` — control plane + static serving (extended)
The bridge gains: a static file handler serving the embedded SPA with SPA-fallback routing and `index.html`
token injection; the `/api/*` command mux; and the `/api/events` SSE handler. All reuse the existing
`originOK` + token checks. The bridge takes a new `Control` interface (the set of `Node` methods the API
needs) so it stays unit-testable against a fake.

### `internal/app` event hub (new, small)
An in-process fan-out: subsystems publish typed events (`presence`, `request`, `audience`, `party-ended`);
SSE subscribers receive them. Bounded per-subscriber buffer; slow/dead subscribers are dropped, never block
a publisher. Guarded by its own mutex; publishers never hold a subsystem lock across a hub send.

### Notification hooks (minimal edits to three files)
- **`internal/signaling` client:** an `OnPresenceChange` callback fired where `c.peers` is updated
  (join/leave) → hub `presence` event.
- **`internal/catalog` service:** an `OnRequest` callback fired when an inbound access request is recorded
  → hub `request` event.
- **`internal/app` party coordinator:** fire on `broadcastAudience` (host) and `OnPartyAudience` (viewer)
  → hub `audience` event. (`OnPartyAudience` is a no-op for UI today; this gives it a surface.)

Each hook is set by `Node` wiring; default nil = no-op, so existing tests are unaffected.

### `webui/` — Nuxt 4 SPA (new)
`ssr: false`. Nuxt UI v4 components. Dashboard shell. Talks only to the loopback bridge. `nuxt generate`
output lands in `webui/dist`, embedded via `//go:embed all:webui/dist`. Built with `web-design-engineer`
craft.

### `cmd/node` (extended)
Starts the bridge bound to `127.0.0.1:0`, prints the loopback URL, and auto-opens the browser
(`--no-open` to suppress). Surfaces the session token via the injected bootstrap (prod) / a printed
`?token=` URL (dev).

### Host self-playback (small new path in `internal/app`)
The Host's browser plays its *own* stream at `/s/{token}/{self}/{cid}/…` to drive the party engine, but
`Node.Playlist`/`Segment` dial a peer session keyed by `host` and there is no session to self. So when
`host == self` they short-circuit to the local media handler (`n.media`) and **skip** the
`policy.Allowed(remote)` check — the access Policy governs *remote* Viewers; the owner always sees their own
Library. This is the host self-playback wiring deferred since Slice 4.

### Unchanged
The `/s/` media path and `Node.Segment` swarm-awareness; the `/party/` role/`hello` protocol (viewer payload
gains only the additive `driftMs`) and the `party`/`swarm` engines; the wire protocol; download/whole-file
integrity.

## Control API Surface

All token-gated, loopback + Origin-checked, JSON. Thin wrappers over `Node`:

| Method & path | Wraps | Returns |
| --- | --- | --- |
| `GET /api/self` | identity / display name | `{nodeId, displayName}` |
| `GET /api/presence` | `client.Peers()` | `[{nodeId, displayName, online}]` |
| `GET /api/library` | local `catalog` | your **Library** `[TitleMeta]` (+ access policy) — feeds the "Your Library" panel and party-start picker |
| `GET /api/peers/{id}/catalog` | `Node.Browse` | `[TitleMeta]` (per-title live-party badge), or **403** when the peer's Policy denies you (→ "Request access") |
| `POST /api/peers/{id}/request-access` | `Node.RequestAccess` | 202 |
| `GET /api/requests` | `Node.PendingRequests` | `[nodeId]` |
| `POST /api/requests/{id}/approve` | `Node.ApproveAccess` | 200 |
| `POST /api/party/start` | `Node.StartParty` | `{partyId}` |
| `POST /api/party/join` | `Node.JoinParty` | 200 |
| `POST /api/party/leave` / `POST /api/party/end` | leave / `EndParty` | 200 |
| `GET /api/events` | event hub | SSE: `presence`, `request`, `audience`, `party-ended` |

Access is **per-peer, not per-title**: `Node.Browse` returns the whole Catalog or `peer.ErrDenied`
(`catalog.Service` enforces a Library-wide Policy), so the handler maps `ErrDenied` → 403 and the SPA shows a
"Request access" state for that peer. The per-title live-party badge reuses the `LiveParty(contentID)`
provider already on the party coordinator (evaluated Host-side when serving Browse).

## The SPA (Nuxt 4 + Nuxt UI v4)

**Shell:** home dashboard (chosen layout) with panels — **Online peers**, **Requests**, **Your Library**,
**Now watching**. Routes:

- `/` — dashboard. Panels hydrate from `/api/presence`, `/api/requests`, `/api/library` (Your Library),
  and client-side now-watching state; live-patched by SSE. Hosting a party is started from a Title in the
  "Your Library" list.
- `/peer/{id}` — browse a peer's Catalog (`/api/peers/{id}/catalog`). If the peer denies you (403), the
  screen shows a **Request access** state for the whole Catalog; once allowed, per-Title actions are
  **Watch**, **Join** (if a live party), **Download**.
- `/watch/{host}/{contentId}` — the player (solo, host, or viewer variant).

**State:** REST snapshots seed the stores; the single SSE connection applies live patches (peer
online/offline, new request, audience change, party ended). One `EventSource`, opened once at app load.

**Token bootstrap:** `token = window.__P2P__?.token ?? new URLSearchParams(location.search).get('token')`,
persisted to `sessionStorage`; attached to all requests, the `/party/` WS URL, and the SSE URL.

## Watch-Party Actuators (the deferred "real browser actuator")

- **Host page:** hls.js plays the host's *own* stream (`/s/{token}/{self}/{cid}/index.m3u8`, served locally
  via the host self-playback path above). `<video>` `play`/`pause`/`seeking`/`timeupdate` map to host
  `playerMsg`s on the `/party/` WS (`hello{role:host}` first) → drives the `party.Host` engine. Audience
  strip from SSE.
- **Viewer page:** hls.js plays `/s/...` (swarm-aware behind `Node.Segment`). Opens `/party/` WS as
  `role:viewer`, sends periodic `report{posMs, playing}`, receives `party.Action {Play, Seek, SeekMS, Rate}`
  **plus an additive `driftMs`** and applies the action — hard-seek (`video.currentTime = SeekMS/1000`)
  beyond threshold vs gentle `playbackRate = Rate` nudge within deadband, per ADR 0004. Transport locked;
  volume/fullscreen kept. The **drift indicator** displays `driftMs` (the host-target gap the `party.Viewer`
  engine already computes in `Decide`, now surfaced on the wire). Audience from SSE.
- **Solo watch:** the same player with a working transport and no audience strip.

The Go side — `serveHostWS`/`serveViewerWS`/`viewerDecide`/`PartyViewerDecide`/`IngestHostPlayer` — already
exists and is virtual-clock-tested; only the browser half is new. The Go fake actuator used in the slice-4
e2e remains the deterministic CI stand-in; the browser actuator is verified by the smoke test below.

## Security & Trust

- Bridge stays **loopback-only** (refuses non-loopback binds) + **Origin-checked** + **token-gated** —
  all existing guards, now also covering `/api/*`, `/`, and `/api/events`.
- The session token is injected into `index.html` at serve time; a cross-origin page cannot read it
  (same-origin policy blocks reading the response body), and the bridge is unreachable off-host.
- SSE and the player WS carry the same token. No new trust surface beyond the existing bridge model.

## Build, Dev & Tooling

- **`webui/`** Nuxt project. `make webui` = `npm ci && npx nuxt generate` → `webui/dist`. `make build`
  runs `make webui` before `go build` (which embeds `dist`). `make test` (Go) is unaffected; `make webui-test`
  runs the Vitest units (blocking) and `make webui-e2e` runs the Playwright smoke (non-blocking). CI gains a
  Node setup + `make webui` step.
- **Dev loop:** `nuxt dev` (Vite) on its own port, proxying `/api`, `/s`, `/party`, `/api/events` to a
  running node's bridge; the node prints a `?token=…` dev bootstrap URL. Production serves the prebuilt
  embedded bundle.
- A committed placeholder `webui/dist` (or a build-tag guard) keeps `go build ./...` working before the
  first JS build, so Go-only contributors and CI Go steps don't break.

## Testing Strategy

- **Control API handlers (Go unit, `httptest` + fake `Control`):** each endpoint's happy path + token/Origin
  rejection + error mapping (e.g. `ErrNotFound` → 404, `ErrDenied` → 403).
- **Event hub (Go unit):** fan-out to multiple subscribers; slow subscriber dropped without blocking;
  publish/subscribe race-clean under `-race`.
- **Notification hooks (Go unit):** presence/request/audience callbacks fire on the right transitions;
  nil-callback (default) path unchanged.
- **SSE handler (Go unit):** subscribes, streams an event, flushes, cleans up on client disconnect.
- **Browser actuator logic (Vitest pure-unit, blocking CI):** the actuator's decision-application is
  extracted into pure TS modules — apply a `party.Action` to a player-like object; format `driftMs`;
  reduce SSE events into store patches — and unit-tested with **no browser, no hls.js**. This mirrors the
  repo's "pure engine behind interfaces, deterministically tested" discipline on the JS side and keeps the
  blocking CI fast and deterministic.
- **Browser smoke (Playwright, one happy path, off the critical path):** start two nodes, each serving its
  own loopback bridge, and drive one browser page per node; presence → browse → request/approve → stream a
  Title → start a party on one, join on the other → assert the viewer converges (drift within band) and the
  Audience renders "2 watching". Run as a **separate, non-blocking** CI job (or a local `make webui-e2e`
  target exercised in the end-of-slice `verify` pass) so a flaky browser run never reds the green bar.
- `go test -race ./...` and the Vitest units stay green and block; the Playwright smoke does not block.

## Success Criteria

1. From a clean `cmd/node` start, the browser opens to the dashboard showing self + online peers, with no
   manual token handling.
2. A user can browse a peer's catalog, request access, approve an inbound request, and stream a title in
   hls.js — all from the UI.
3. Host starts a party and drives playback; a second node joins as viewer and stays hard-synced (drift
   within the ADR-0004 band) with a locked transport; both render the Audience.
4. The binary still ships as a single static executable (`go build` embeds the SPA); `go test -race ./...`
   and the Vitest units stay green (blocking); the Playwright smoke passes off the critical path.
5. `/s/` is byte-for-byte unchanged and the `/party/` role protocol is unchanged (only the additive
   `driftMs` field is added to the viewer payload); no engine logic moved.

## Out of Scope (this slice)

- **Viewer-requested pause/seek** (host accepts) — viewer transport stays locked; deferred.
- **PartyInvite accept-UI** — `OnPartyInvite` stays a no-op; joining is via the live-party badge / dashboard.
- **ABR / multi-rendition** (single rendition; `/s/` and the swarm rendition seam unchanged).
- **NAT hardening:** trickle ICE + TURN.
- **Image-subtitle burn-in** (PGS/VOBSUB).
- **Slice-5 review minors** (gossip `Dial` timeout, dead `SwarmDial` code, `gossipLoop` stop on `Close`).
- **Connection-lifecycle minors** (cancel in-flight Pings on `Close`; late-session-on-`Close`).
- Packaging/installers, auto-update, multi-window, and any non-loopback exposure.
