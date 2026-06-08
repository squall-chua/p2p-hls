# Browser UI: control-plane API, SPA serving, and auth model

**Status:** accepted

The backend is feature-complete through Slice 5 but has no browser UI — `cmd/node` is a CLI and the
loopback bridge serves only media (`/s/`) and the player WebSocket (`/party/`). Slice 6 adds the full web
UI. Four interdependent decisions define how it is served, how it talks to the Node, how it authenticates,
and how the Host plays its own Library. They are recorded together.

## Decision 1 — The UI is a Nuxt 4 client-only static SPA embedded via `go:embed`; no Node runtime ships

The UI is built with Nuxt 4 (`ssr: false`) and Nuxt UI v4, generated to a static bundle (`nuxt generate`)
that the Go bridge serves from `//go:embed`. The single static binary is preserved; the only change to the
build is a Node step (`make webui`) that runs before `go build`. No JavaScript server (Nitro) is deployed.

**Why:**
- The bridge is a loopback Go HTTP server with no Node process; an SSR/Nitro runtime would break the
  single-binary model that the whole product is built on (pure-Go SQLite, `go:embed`, one static binary).
  A client-only SPA keeps all runtime logic in the existing Go process and ships zero JS server.
- A framework (over hand-written vanilla JS) was chosen for a 5-screen app with modals, tables, a player,
  and live-updating panels; Nuxt UI v4 supplies accessible components so the slice spends its effort on the
  P2P-specific surfaces, not on rebuilding a component kit.

**Rejected alternatives:** vanilla JS + hls.js with no build step (keeps the pure-Go toolchain, but hand-
rolling the component layer for this many stateful screens is more slice cost than the build step it avoids);
an SSR/Nitro deployment (incompatible with the loopback-only, single-binary model).

**Consequences:**
- The build gains a Node toolchain dependency and a `make webui` step; CI gains a Node setup step. A
  committed placeholder `webui/dist` keeps `go build ./...` working before the first JS build.
- Dev runs `nuxt dev` (Vite) proxying `/api`, `/s`, `/party`, `/api/events` to a running node.

## Decision 2 — The control plane is REST commands + a single SSE event stream, not a WebSocket RPC channel

Control commands (browse, request access, approve, start/join/leave/end party, list presence and Library)
are plain JSON request/response handlers on the bridge under `/api/*`. Server-pushed changes (presence,
incoming access requests, Audience) are delivered over one Server-Sent-Events stream, `GET /api/events`,
fed by an in-process event hub. The player keeps its own separate `/party/` WebSocket.

**Why:**
- The commands are naturally request/response and map one-to-one onto existing `Node` methods, so plain
  HTTP handlers stay stateless and unit-testable (`httptest` + a fake `Control`) with no request/response
  correlation machinery.
- The only push need is a low-rate fan-out of state changes; SSE covers it with stdlib (`http.Flusher`) on
  the server and `EventSource` in the browser, and degrades to a simple reconnect. A second bidirectional
  WebSocket alongside `/party/` would add RPC plumbing for no benefit.
- The push sources are event-driven: small change-callbacks on the signaling client (presence), catalog
  service (incoming request), and party coordinator (Audience) publish to the hub. This avoids a server-side
  polling loop and gives instant updates. (`OnPartyAudience`, a no-op for the UI until now, gains a surface.)

**Rejected alternatives:** a single control WebSocket carrying both commands and events (unifies transport
but reintroduces request-id correlation and a second socket); server-side polling + diff to feed SSE (zero
edits to the three subsystems, but ~1s latency and a perpetual loop) — rejected in favour of the push hooks.

**Consequences:**
- Three existing files gain a nil-defaulted notification callback (guarded by their existing mutexes;
  publishers never hold a subsystem lock across a hub send). The hub drops slow/dead subscribers rather than
  blocking a publisher.

## Decision 3 — Auth is a same-origin injected token over the existing loopback + Origin guards

The bridge stays loopback-only (refuses non-loopback binds) and Origin-checked. When it serves `index.html`
it injects the session token as `window.__P2P__ = {token, nodeId, name}`; the SPA authenticates every call
with it — an `Authorization` header for `/api/*`, a `?token=` query for `/api/events` (EventSource cannot set
headers), and the existing path token for `/s/` and `/party/`. Dev reads the token from a `?token=` bootstrap
URL the node prints.

**Why:**
- The bridge is unreachable off-host, and the Origin check rejects cross-origin browser requests; a cross-
  origin page also cannot read the injected token (same-origin policy blocks reading the response body). So
  the token defends against other browser tabs/sites, which is the actual threat for a loopback server.
- A local native process can already read the loopback port and, like the existing `/s/` and `/party/`
  path-tokens, could extract the token — but a local process is outside the loopback threat model (it can do
  worse directly). This introduces no regression versus the path-token model already shipped.

**Rejected alternative:** cookie/session auth — needs server-side session state and CSRF handling for a
single-user loopback app, where an injected per-process token over the existing guards is simpler and
equivalent in protection.

## Decision 4 — Host self-playback serves the Host's own Library locally, bypassing the remote-access policy

When the bridge streams with `host == self`, `Node.Playlist`/`Segment` short-circuit to the local media
handler and **skip** the `policy.Allowed(remote)` check, rather than opening a peer session to self.

**Why:**
- `Node.Playlist`/`Segment` dial a peer session keyed by `host`; there is no session to oneself, so the Host's
  own player (which plays `/s/{token}/{self}/{cid}/…` to drive the party engine) needs a local path.
- The access Policy governs *remote* Viewers; the owner always sees their own Library. Routing self-playback
  through `policy.Allowed(self)` would force the owner into their own allow-list, which is incoherent.

**Consequences:**
- This is the wiring that finally lets a Host watch (and therefore drive a Watch Party for) its own Title in
  the browser — the host self-playback path deferred since Slice 4.
- The viewer player's downstream `/party/` message gains one additive field (`driftMs`) so the browser can
  display sync confidence; the engine already computes it. The role/`hello` protocol is otherwise unchanged.
