# Slice 6b — Browser UI (Nuxt SPA + actuators) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. For the visual/component work, invoke the **nuxt-ui** and **web-design-engineer** skills.

**Goal:** Build the embedded Nuxt 4 SPA — dashboard, peer-browse, and watch screens — plus the in-browser host/viewer Watch-Party actuators, replacing Slice 6a's placeholder bundle and wiring the JS build/test into the project.

**Architecture:** A client-only Nuxt 4 SPA (`ssr: false`) using Nuxt UI v4, talking only to the loopback bridge: REST `/api/*` for commands, one `EventSource` on `/api/events` for live patches, hls.js for playback, and the existing `/party/` WebSocket for sync. The party engine stays authoritative in Go; the browser is a thin actuator. The viewer's downstream `party.Action` gains one additive `driftMs` field. `nuxt generate` output is copied into `internal/bridge/dist` and embedded by Slice 6a's static handler.

**Tech Stack:** Nuxt 4, Vue 3, `@nuxt/ui` v4, hls.js, Vitest (pure-logic units), Playwright (one non-blocking smoke), Node 20+ toolchain. Go side: one additive field in `internal/party`.

**Depends on:** **Slice 6a** (control plane) merged/complete — the `/api/*` contract, SSE hub, token bootstrap, and `cmd/node` bridge launch. Reference: spec `docs/superpowers/specs/2026-06-08-slice-6-web-ui-design.md`; ADR `docs/adr/0006-browser-ui-control-plane.md`. Commit style: short point-form, **no** `Co-Authored-By`.

**Wire contracts (from Slice 6a + `internal/app/party.go`):**
- Bootstrap: `window.__P2P__ = {token, nodeId, name}` (prod, injected) or `?token=` (dev).
- REST: `Authorization: Bearer <token>`; SSE: `/api/events?token=<token>`; media + party WS: path token (`/s/<token>/…`, `/party/<token>`).
- Player WS up-messages (`playerMsg`): `{"type":"hello","role":"host"|"viewer"}`, then host `{"type":"play"|"pause"|"seek"|"report","posMs":N,"playing":b}`; viewer `{"type":"report","posMs":N,"playing":b}`.
- Player WS down-message (viewer only), the `party.Action` JSON after Task 1: `{"play":b,"seek":b,"seekMs":N,"rate":f,"driftMs":N}`.

---

## Task 1: Go — add additive `driftMs` to `party.Action`

**Files:**
- Modify: `internal/party/party.go` (add JSON tags + `DriftMS` field to `Action`)
- Modify: `internal/party/viewer.go` (`Decide` populates `DriftMS`)
- Test: `internal/party/viewer_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
// internal/party/viewer_test.go (append)
func TestDecideReportsDrift(t *testing.T) {
	cfg := DefaultConfig()
	v := NewViewer(testClock{}, cfg)
	now := time.Unix(100, 0)
	// host at 10_000ms, playing, seq 1
	v.OnState(State{PartyID: "p", Playing: true, PositionMS: 10_000, Rate: 1, Seq: 1}, now)
	// viewer reports 10_300ms -> ~+300ms drift (assuming owd 0 in test clock)
	act := v.Decide(10_300, true, now)
	if act.DriftMS == 0 {
		t.Fatalf("expected non-zero drift, got %+v", act)
	}
}
```

Match `testClock`/`DefaultConfig` to the existing helpers in `internal/party` test files (inspect `viewer_test.go` / `party_test.go` for the exact names; reuse them).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestDecideReportsDrift -v`
Expected: FAIL — `DriftMS` undefined.

- [ ] **Step 3: Write the implementation**

`internal/party/party.go` — give `Action` JSON tags and the new field (no production JSON consumer exists yet, so adding tags is safe and gives the browser a clean lowercase contract):

```go
type Action struct {
	Play    bool    `json:"play"`
	Seek    bool    `json:"seek"`
	SeekMS  int64   `json:"seekMs"`
	Rate    float64 `json:"rate"`
	DriftMS int64   `json:"driftMs"` // viewer-ahead(+)/behind(-) gap vs the host target
}
```

`internal/party/viewer.go` — compute `drift` once and stamp it on every returned `Action`. Replace the `switch` in `Decide`:

```go
	h := v.expectedHostPosLocked(now)
	drift := playerPosMS - h // + => viewer ahead
	abs := drift
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs > v.cfg.SeekThresholdMS:
		return Action{Play: v.last.Playing, Seek: true, SeekMS: h, Rate: 1.0, DriftMS: drift}
	case abs > v.cfg.DeadbandMS:
		rate := clamp(1.0-v.cfg.Kp*(float64(drift)/1000.0), v.cfg.MinRate, v.cfg.MaxRate)
		return Action{Play: v.last.Playing, Rate: rate, DriftMS: drift}
	default:
		return Action{Play: v.last.Playing, Rate: 1.0, DriftMS: drift}
	}
```

(The no-state early return keeps `DriftMS: 0`, which is correct — no host target yet.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/party/ ./internal/app/ -race`
Expected: PASS. If any existing test asserted capitalized JSON keys for `Action`, update it to the lowercase tags.

- [ ] **Step 5: Commit**

```bash
git add internal/party/party.go internal/party/viewer.go internal/party/viewer_test.go
git commit -m "party: viewer Action carries additive driftMs (json-tagged)"
```

---

## Task 2: Scaffold the Nuxt 4 SPA

**Files:**
- Create: `webui/` (Nuxt project), `webui/nuxt.config.ts`, `webui/app.html`, `webui/package.json`, `webui/.gitignore`
- Modify: root `.gitignore` (ignore `webui/node_modules`, `webui/.nuxt`, `webui/.output`, `internal/bridge/dist/*` except the placeholder)

- [ ] **Step 1: Initialize the project**

Run:
```bash
cd /home/mwchua/p2p-hls
npx nuxi@latest init webui --packageManager npm --no-gitInit
cd webui && npx nuxi@latest module add ui   # adds @nuxt/ui v4 + peer deps
npm install hls.js
```
Expected: `webui/` with Nuxt 4 + `@nuxt/ui` in `package.json`.

- [ ] **Step 2: Configure for static SPA + dev proxy**

`webui/nuxt.config.ts`:

```ts
export default defineNuxtConfig({
  ssr: false,
  modules: ['@nuxt/ui'],
  devtools: { enabled: true },
  // Dev: proxy the Go control plane (run `node` with --bridge-addr 127.0.0.1:8787, Task 9).
  nitro: {
    devProxy: {
      '/api': { target: 'http://127.0.0.1:8787', changeOrigin: true, ws: false },
      '/s': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/party': { target: 'http://127.0.0.1:8787', changeOrigin: true, ws: true },
    },
  },
})
```

`webui/app.html` (root HTML template — preserves the bridge's injection marker):

```html
<!DOCTYPE html>
<html {{ HTML_ATTRS }}>
  <head>{{ HEAD }}</head>
  <body {{ BODY_ATTRS }}>
    <!--__P2P_BOOTSTRAP__-->
    {{ APP }}
  </body>
</html>
```

- [ ] **Step 3: Verify dev build runs**

Run: `cd webui && npx nuxt generate`
Expected: succeeds, output under `webui/.output/public/` including `index.html` containing the `<!--__P2P_BOOTSTRAP__-->` marker.

- [ ] **Step 4: gitignore the build artifacts**

Append to root `.gitignore`:

```
webui/node_modules/
webui/.nuxt/
webui/.output/
internal/bridge/dist/assets/
```

(The committed `internal/bridge/dist/index.html` placeholder stays tracked; generated assets are ignored and regenerated by `make webui`.)

- [ ] **Step 5: Commit**

```bash
git add webui/nuxt.config.ts webui/app.html webui/package.json webui/package-lock.json webui/.gitignore .gitignore webui/tsconfig.json webui/app.vue
git commit -m "webui: scaffold Nuxt 4 client-only SPA with @nuxt/ui + hls.js"
```

---

## Task 3: Token bootstrap + API client composable

**Files:**
- Create: `webui/app/composables/useBridge.ts`
- Test: `webui/test/useBridge.spec.ts` (Vitest)
- Modify: `webui/package.json` (add vitest + test script), create `webui/vitest.config.ts`

- [ ] **Step 1: Add Vitest**

Run: `cd webui && npm install -D vitest @vue/test-utils happy-dom`

`webui/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config'
export default defineConfig({ test: { environment: 'happy-dom' } })
```

Add to `webui/package.json` scripts: `"test": "vitest run"`, `"test:watch": "vitest"`.

- [ ] **Step 2: Write the failing test**

```ts
// webui/test/useBridge.spec.ts
import { describe, it, expect } from 'vitest'
import { readBootstrap } from '../app/composables/useBridge'

describe('readBootstrap', () => {
  it('prefers window.__P2P__', () => {
    const b = readBootstrap({ __P2P__: { token: 't1', nodeId: 'n1', name: 'A' } } as any, '?token=zzz')
    expect(b.token).toBe('t1')
    expect(b.nodeId).toBe('n1')
  })
  it('falls back to ?token= in dev', () => {
    const b = readBootstrap({} as any, '?token=devtok')
    expect(b.token).toBe('devtok')
  })
})
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd webui && npx vitest run test/useBridge.spec.ts`
Expected: FAIL — module not found.

- [ ] **Step 4: Write the implementation**

```ts
// webui/app/composables/useBridge.ts
export interface Bootstrap { token: string; nodeId: string; name: string }

// readBootstrap resolves the session token from the injected global (prod) or the
// URL query (dev). Pure + injectable for tests.
export function readBootstrap(win: any, search: string): Bootstrap {
  const g = win?.__P2P__
  if (g?.token) return { token: g.token, nodeId: g.nodeId ?? '', name: g.name ?? '' }
  const t = new URLSearchParams(search).get('token') ?? ''
  return { token: t, nodeId: '', name: '' }
}

let cached: Bootstrap | null = null
function boot(): Bootstrap {
  if (cached) return cached
  cached = readBootstrap(typeof window !== 'undefined' ? window : {}, typeof location !== 'undefined' ? location.search : '')
  return cached
}

// useBridge returns the typed REST + SSE client, authenticated with the token.
export function useBridge() {
  const { token, nodeId, name } = boot()
  const headers = { Authorization: `Bearer ${token}` }
  const api = <T>(path: string, init?: RequestInit) =>
    fetch(path, { ...init, headers: { ...headers, ...(init?.headers || {}) } }).then(async (r) => {
      if (!r.ok) throw Object.assign(new Error(`api ${path} ${r.status}`), { status: r.status })
      return (r.status === 204 ? undefined : await r.json()) as T
    })
  return {
    token, nodeId, name,
    self: () => api<{ nodeId: string; displayName: string }>('/api/self'),
    presence: () => api<any[]>('/api/presence'),
    library: () => api<any[]>('/api/library'),
    catalog: (id: string) => api<any[]>(`/api/peers/${id}/catalog`),
    requestAccess: (id: string, message: string) =>
      api<void>(`/api/peers/${id}/request-access`, { method: 'POST', body: JSON.stringify({ message }) }),
    requests: () => api<string[]>('/api/requests'),
    approve: (id: string) => api<void>(`/api/requests/${id}/approve`, { method: 'POST' }),
    startParty: (contentId: string) => api<{ partyId: string }>('/api/party/start', { method: 'POST', body: JSON.stringify({ contentId }) }),
    joinParty: (hostNodeId: string, contentId: string) => api<void>('/api/party/join', { method: 'POST', body: JSON.stringify({ hostNodeId, contentId }) }),
    leaveParty: () => api<void>('/api/party/leave', { method: 'POST' }),
    endParty: () => api<void>('/api/party/end', { method: 'POST' }),
    streamURL: (host: string, contentId: string) => `/s/${token}/${host}/${contentId}/index.m3u8`,
    eventsURL: () => `/api/events?token=${encodeURIComponent(token)}`,
    partyWSURL: () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      return `${proto}://${location.host}/party/${token}`
    },
  }
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd webui && npx vitest run test/useBridge.spec.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add webui/app/composables/useBridge.ts webui/test/useBridge.spec.ts webui/vitest.config.ts webui/package.json webui/package-lock.json
git commit -m "webui: token bootstrap + authenticated bridge client (vitest)"
```

---

## Task 4: Pure actuator logic + drift formatting (Vitest)

This is the deterministically-tested core, mirroring the Go discipline. No DOM, no hls.js.

**Files:**
- Create: `webui/app/lib/actuator.ts`
- Test: `webui/test/actuator.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
// webui/test/actuator.spec.ts
import { describe, it, expect } from 'vitest'
import { planViewerActuation, hostMessageFor, formatDrift } from '../app/lib/actuator'

describe('planViewerActuation', () => {
  it('hard-seeks when action.seek', () => {
    const p = planViewerActuation({ play: true, seek: true, seekMs: 42000, rate: 1, driftMs: 1500 })
    expect(p).toEqual({ seekTo: 42, rate: 1, play: true })
  })
  it('rate-nudges without seeking', () => {
    const p = planViewerActuation({ play: true, seek: false, seekMs: 0, rate: 1.05, driftMs: -200 })
    expect(p).toEqual({ seekTo: null, rate: 1.05, play: true })
  })
  it('pauses when action.play is false', () => {
    const p = planViewerActuation({ play: false, seek: false, seekMs: 0, rate: 1, driftMs: 0 })
    expect(p.play).toBe(false)
  })
})

describe('hostMessageFor', () => {
  it('maps a play event', () => {
    expect(hostMessageFor('play', { currentTime: 12.4, paused: false })).toEqual({ type: 'play', posMs: 12400, playing: true })
  })
  it('maps a timeupdate to a report', () => {
    expect(hostMessageFor('timeupdate', { currentTime: 30, paused: false })).toEqual({ type: 'report', posMs: 30000, playing: true })
  })
})

describe('formatDrift', () => {
  it('renders signed seconds', () => {
    expect(formatDrift(200)).toBe('+0.2s')
    expect(formatDrift(-1500)).toBe('-1.5s')
    expect(formatDrift(0)).toBe('±0.0s')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd webui && npx vitest run test/actuator.spec.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

```ts
// webui/app/lib/actuator.ts
export interface ViewerAction { play: boolean; seek: boolean; seekMs: number; rate: number; driftMs: number }
export interface Actuation { seekTo: number | null; rate: number; play: boolean }

// planViewerActuation turns a server Action into concrete player operations.
export function planViewerActuation(a: ViewerAction): Actuation {
  return { seekTo: a.seek ? a.seekMs / 1000 : null, rate: a.rate || 1, play: a.play }
}

export type HostEvent = 'play' | 'pause' | 'seek' | 'timeupdate'
export interface PlayerSnapshot { currentTime: number; paused: boolean }
export interface HostMessage { type: 'play' | 'pause' | 'seek' | 'report'; posMs: number; playing: boolean }

// hostMessageFor maps a <video> event into the player WS up-message.
export function hostMessageFor(ev: HostEvent, p: PlayerSnapshot): HostMessage {
  const type = ev === 'timeupdate' ? 'report' : ev
  return { type, posMs: Math.round(p.currentTime * 1000), playing: !p.paused }
}

// formatDrift renders a signed-seconds drift indicator, e.g. "+0.2s".
export function formatDrift(driftMs: number): string {
  const s = driftMs / 1000
  const sign = driftMs > 0 ? '+' : driftMs < 0 ? '-' : '±'
  return `${sign}${Math.abs(s).toFixed(1)}s`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd webui && npx vitest run test/actuator.spec.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add webui/app/lib/actuator.ts webui/test/actuator.spec.ts
git commit -m "webui: pure viewer/host actuator logic + drift formatting (vitest)"
```

---

## Task 5: Live store + SSE client

**Files:**
- Create: `webui/app/lib/store.ts` (reactive state + `applyEvent` reducer), `webui/app/composables/useLiveData.ts` (SSE wiring)
- Test: `webui/test/store.spec.ts`

- [ ] **Step 1: Write the failing test**

```ts
// webui/test/store.spec.ts
import { describe, it, expect, vi } from 'vitest'
import { refetchFor } from '../app/lib/store'

describe('refetchFor', () => {
  it('maps event types to the snapshots to refetch', () => {
    expect(refetchFor('presence')).toContain('presence')
    expect(refetchFor('request')).toContain('requests')
    expect(refetchFor('audience')).toContain('audience')
    expect(refetchFor('party-ended')).toContain('audience')
    expect(refetchFor('unknown')).toEqual([])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd webui && npx vitest run test/store.spec.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

```ts
// webui/app/lib/store.ts
// refetchFor maps an SSE event type to which snapshot(s) the SPA should refetch.
// Events carry only a type (Slice 6a); the SPA pulls the authoritative snapshot.
export function refetchFor(type: string): string[] {
  switch (type) {
    case 'presence': return ['presence']
    case 'request': return ['requests']
    case 'audience': return ['audience']
    case 'party-ended': return ['audience']
    default: return []
  }
}
```

```ts
// webui/app/composables/useLiveData.ts
import { useBridge } from './useBridge'
import { refetchFor } from '../lib/store'

// useLiveData opens the single SSE stream and invokes onRefetch(kind) per event.
export function useLiveData(onRefetch: (kind: string) => void) {
  const bridge = useBridge()
  let es: EventSource | null = null
  function start() {
    es = new EventSource(bridge.eventsURL())
    es.onmessage = (m) => {
      try {
        const ev = JSON.parse(m.data)
        for (const kind of refetchFor(ev.type)) onRefetch(kind)
      } catch { /* ignore malformed */ }
    }
  }
  function stop() { es?.close(); es = null }
  return { start, stop }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd webui && npx vitest run test/store.spec.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add webui/app/lib/store.ts webui/app/composables/useLiveData.ts webui/test/store.spec.ts
git commit -m "webui: SSE live-data client + refetch reducer (vitest)"
```

---

## Task 6: Dashboard screen (`/`)

**Files:**
- Create: `webui/app/pages/index.vue`, `webui/app/app.vue` (shell), `webui/app/components/PeerList.vue`, `RequestList.vue`, `LibraryPanel.vue`

**Skill:** invoke **nuxt-ui** for component APIs (`UCard`, `UButton`, `UTable`, `UBadge`, `UModal`, `useToast`) and **web-design-engineer** for layout/visual craft. Match the dashboard mock from brainstorming (`.superpowers/brainstorm/.../shell-layout.html` option C: panels for Online peers / Requests / Your Library / Now watching).

- [ ] **Step 1: Build the shell + dashboard**

`webui/app/app.vue`:

```vue
<template>
  <UApp>
    <NuxtPage />
  </UApp>
</template>
```

`webui/app/pages/index.vue` — four-panel dashboard. Hydrate from REST, live-patch via `useLiveData`:

```vue
<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

const bridge = useBridge()
const peers = ref<any[]>([])
const requests = ref<string[]>([])
const library = ref<any[]>([])

async function refetch(kind = 'all') {
  if (kind === 'all' || kind === 'presence') peers.value = await bridge.presence()
  if (kind === 'all' || kind === 'requests') requests.value = await bridge.requests()
  if (kind === 'all') library.value = await bridge.library()
}
onMounted(async () => {
  await refetch('all')
  useLiveData((k) => refetch(k)).start()
})
</script>

<template>
  <div class="p-6 grid grid-cols-2 gap-4">
    <UCard><template #header>Online peers</template>
      <PeerList :peers="peers" />
    </UCard>
    <UCard><template #header>Requests</template>
      <RequestList :requests="requests" @approved="refetch('requests')" />
    </UCard>
    <UCard><template #header>Your Library</template>
      <LibraryPanel :titles="library" />
    </UCard>
    <UCard><template #header>Now watching</template>
      <p class="text-sm text-gray-500">nothing yet</p>
    </UCard>
  </div>
</template>
```

`PeerList.vue` (each peer links to `/peer/{id}`), `RequestList.vue` (each request has an Approve button calling `bridge.approve(id)` then emitting `approved`), `LibraryPanel.vue` (each Title links to `/watch/{self}/{cid}` and a "Start party" button calling `bridge.startParty(cid)`). Use `useBridge().nodeId` for self.

- [ ] **Step 2: Verify it renders against a running node**

Run (two terminals):
```bash
# terminal 1: a signal server + node bound to the dev port (Task 9 adds --bridge-addr)
go run ./cmd/signal-server &
go run ./cmd/node --name Alice --bridge-addr 127.0.0.1:8787 --no-open
# terminal 2:
cd webui && npx nuxt dev
```
Open the printed Nuxt dev URL; the dashboard loads self + online peers. (No assertion here — visual check; the automated guard is the Vitest units + the Playwright smoke in Task 10.)

- [ ] **Step 3: Commit**

```bash
git add webui/app/app.vue webui/app/pages/index.vue webui/app/components/PeerList.vue webui/app/components/RequestList.vue webui/app/components/LibraryPanel.vue
git commit -m "webui: dashboard shell + peers/requests/library panels"
```

---

## Task 7: Peer browse screen (`/peer/[id]`)

**Files:**
- Create: `webui/app/pages/peer/[id].vue`

- [ ] **Step 1: Build the browse screen**

Fetch `bridge.catalog(id)`. On a 403 (`err.status === 403`), render a "Request access" state with a message box → `bridge.requestAccess(id, message)` + a `useToast()` confirmation. On success, list Titles; per-Title actions: **Watch** → `/watch/{id}/{cid}`, **Join** (when `title.partyLive`) → `bridge.joinParty(id, cid)` then `/watch/{id}/{cid}`, **Download** → open `/s/{token}/{id}/{cid}/download` if the download route exists (else hide; raw-file download wiring is existing bridge behaviour — confirm the path, otherwise defer the button).

```vue
<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
const route = useRoute(); const bridge = useBridge()
const id = route.params.id as string
const titles = ref<any[]>([]); const denied = ref(false); const message = ref('')
async function load() {
  try { titles.value = await bridge.catalog(id); denied.value = false }
  catch (e: any) { if (e.status === 403) denied.value = true; else throw e }
}
async function request() { await bridge.requestAccess(id, message.value); useToast().add({ title: 'Request sent' }) }
onMounted(load)
</script>

<template>
  <div class="p-6">
    <div v-if="denied">
      <p class="mb-2">This peer's Library is restricted.</p>
      <UInput v-model="message" placeholder="Optional message" class="mb-2" />
      <UButton @click="request">Request access</UButton>
    </div>
    <div v-else class="grid gap-3">
      <UCard v-for="t in titles" :key="t.contentId">
        <div class="flex items-center justify-between">
          <div>{{ t.displayTitle }}
            <UBadge v-if="t.partyLive" color="primary">● Party · {{ t.partyViewers }}</UBadge>
          </div>
          <div class="flex gap-2">
            <UButton :to="`/watch/${id}/${t.contentId}`">Watch</UButton>
            <UButton v-if="t.partyLive" color="primary" @click="join(t.contentId)">Join</UButton>
          </div>
        </div>
      </UCard>
    </div>
  </div>
</template>
```

Add the `join` method: `async function join(cid) { await bridge.joinParty(id, cid); navigateTo(`/watch/${id}/${cid}`) }`.

- [ ] **Step 2: Verify** (visual, against the running node from Task 6).

- [ ] **Step 3: Commit**

```bash
git add webui/app/pages/peer/[id].vue
git commit -m "webui: peer browse screen (catalog, 403 request-access, join)"
```

---

## Task 8: Watch screen + host/viewer actuators (`/watch/[host]/[contentId]`)

**Files:**
- Create: `webui/app/pages/watch/[host]/[contentId].vue`, `webui/app/lib/player.ts` (hls.js + WS wiring shell using Task 4's pure logic), `webui/app/components/AudienceStrip.vue`

This is the deferred "real browser actuator". The pure decisions live in `actuator.ts` (Task 4); this is the thin I/O shell.

- [ ] **Step 1: Player wiring module**

```ts
// webui/app/lib/player.ts
import Hls from 'hls.js'
import { hostMessageFor, planViewerActuation, type ViewerAction } from './actuator'

export type Role = 'solo' | 'host' | 'viewer'

// attachPlayer wires hls.js to <video> and, for host/viewer, the /party WS.
// onDrift is called with each viewer driftMs for the indicator.
export function attachPlayer(opts: {
  video: HTMLVideoElement
  src: string
  role: Role
  wsURL: string
  onDrift?: (driftMs: number) => void
}) {
  const hls = new Hls()
  hls.loadSource(opts.src)
  hls.attachMedia(opts.video)
  if (opts.role === 'solo') return { close: () => hls.destroy() }

  const ws = new WebSocket(opts.wsURL)
  ws.onopen = () => ws.send(JSON.stringify({ type: 'hello', role: opts.role }))

  if (opts.role === 'host') {
    const send = (ev: 'play' | 'pause' | 'seek' | 'timeupdate') =>
      ws.readyState === ws.OPEN && ws.send(JSON.stringify(hostMessageFor(ev, opts.video)))
    opts.video.addEventListener('play', () => send('play'))
    opts.video.addEventListener('pause', () => send('pause'))
    opts.video.addEventListener('seeked', () => send('seek'))
    opts.video.addEventListener('timeupdate', () => send('timeupdate'))
  } else {
    // viewer: report position; apply server Actions
    const report = setInterval(() => {
      if (ws.readyState === ws.OPEN)
        ws.send(JSON.stringify({ type: 'report', posMs: Math.round(opts.video.currentTime * 1000), playing: !opts.video.paused }))
    }, 500)
    ws.onmessage = (m) => {
      const a = JSON.parse(m.data) as ViewerAction
      const plan = planViewerActuation(a)
      if (plan.seekTo !== null) opts.video.currentTime = plan.seekTo
      opts.video.playbackRate = plan.rate
      if (plan.play && opts.video.paused) opts.video.play().catch(() => {})
      if (!plan.play && !opts.video.paused) opts.video.pause()
      opts.onDrift?.(a.driftMs)
    }
    ws.addEventListener('close', () => clearInterval(report))
  }
  return { close: () => { ws.close(); hls.destroy() } }
}
```

- [ ] **Step 2: Watch page**

`webui/app/pages/watch/[host]/[contentId].vue` — determine role (`host === self` ⇒ host if a party is being hosted else solo; otherwise viewer), build `src = bridge.streamURL(host, cid)`, mount `attachPlayer`, lock the viewer transport (`<video>` without `controls` for viewer; show drift via `formatDrift`), render `AudienceStrip` from the audience snapshot, and a Leave/End button (`bridge.leaveParty()` / `bridge.endParty()`).

```vue
<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { attachPlayer } from '~/lib/player'
import { formatDrift } from '~/lib/actuator'

const route = useRoute(); const bridge = useBridge()
const host = route.params.host as string; const cid = route.params.contentId as string
const isSelf = host === bridge.nodeId
const role = isSelf ? 'host' : 'viewer' // solo vs host distinction: see note
const video = ref<HTMLVideoElement>()
const drift = ref(0)
let handle: { close: () => void } | null = null

onMounted(() => {
  handle = attachPlayer({
    video: video.value!, src: bridge.streamURL(host, cid), role,
    wsURL: bridge.partyWSURL(), onDrift: (d) => (drift.value = d),
  })
})
onBeforeUnmount(() => handle?.close())
</script>

<template>
  <div class="p-6">
    <video ref="video" :controls="role !== 'viewer'" class="w-full rounded" />
    <div v-if="role === 'viewer'" class="mt-2 text-sm text-gray-500">
      Synced · {{ formatDrift(drift) }}
    </div>
    <AudienceStrip v-if="role !== 'solo'" :host="host" :content-id="cid" />
  </div>
</template>
```

**Note (solo vs host):** a Title you open from your own Library without starting a party should play `solo` (working transport, no WS). When you click "Start party" it becomes `host`. Track this with a query flag (`/watch/{self}/{cid}?party=1`) or a small state: if `isSelf && !route.query.party` ⇒ `solo`. Implement whichever the dashboard/library "Start party" flow sets; keep `solo` as the default for self-watch.

`AudienceStrip.vue`: fetches audience and listens via `useLiveData` for `audience` events. **Audience source:** Slice 6a publishes an `audience` SSE event but the snapshot endpoint is not in the 6a API. Add `GET /api/party/audience` to Slice 6a's handlers (or fold here): returns `{members:[{nodeId,displayName}]}` from the party coordinator's current host/viewer audience. **This is a gap to close — see Task 8a.**

- [ ] **Step 3: Verify** (visual: host drives, viewer follows, drift shows).

- [ ] **Step 4: Commit**

```bash
git add webui/app/pages/watch webui/app/lib/player.ts webui/app/components/AudienceStrip.vue
git commit -m "webui: watch screen + host/viewer party actuators (hls.js + /party WS)"
```

---

## Task 8a: Go — audience snapshot endpoint (gap closure)

**Files:**
- Modify: `internal/app/party.go` (add `audienceView()` returning members), `internal/app/control.go` (`Audience() []bridge.PeerView`), `internal/bridge/api.go` (`GET /api/party/audience`), and `bridge.Control` (add `Audience()`)
- Test: `internal/bridge/api_test.go`, `internal/app/party_test.go`

The SPA needs to *read* the current Audience (the SSE `audience` event only says "something changed"). Add a snapshot endpoint.

- [ ] **Step 1: Write the failing test**

```go
// internal/bridge/api_test.go (append) — extend fakeControl with Audience()
func (f *fakeControl) Audience() []bridge.PeerView { return f.presence }

func TestAPIAudience(t *testing.T) {
	c := &fakeControl{presence: []bridge.PeerView{{NodeID: "h", DisplayName: "Host"}}}
	_, base := newTestBridge(t, c)
	var got []bridge.PeerView
	json.NewDecoder(apiGET(t, base, "/api/party/audience").Body).Decode(&got)
	if len(got) != 1 { t.Fatalf("audience %+v", got) }
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/bridge/ -run TestAPIAudience -v` → FAIL (`Audience` not in `Control`; route missing).

- [ ] **Step 3: Implement**

Add `Audience() []PeerView` to the `Control` interface (in `api.go`). Add the route in `handleAPI` (in the `party/` GET branch — note start/join/leave/end are POST; add a GET arm):

```go
	case path == "party/audience" && r.Method == http.MethodGet:
		writeJSON(w, c.Audience())
```

`internal/app/party.go` — add a method returning the current audience members (host side: `h.Members()`; viewer side: the last `PartyAudience` seen — store it in `OnPartyAudience`). Map to `[]bridge.PeerView` in `control.go`:

```go
func (n *Node) Audience() []bridge.PeerView {
	for _, m := range n.party.audienceView() {
		// map AudienceMember{NodeID,DisplayName} -> bridge.PeerView{Online:true}
	}
	// ...
}
```

Implement `partyCoordinator.audienceView() []*peerv1.AudienceMember`: if hosting, build from `pc.host.Members()`; if viewing, return the stored last audience (add a `lastAudience` field set in `OnPartyAudience`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/bridge/ ./internal/app/ -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/party.go internal/app/control.go internal/bridge/api.go internal/bridge/api_test.go
git commit -m "bridge+app: GET /api/party/audience snapshot for the UI"
```

---

## Task 9: Build wiring — Makefile, dev port, embed copy

**Files:**
- Modify: root `Makefile` (add `webui`, `webui-test`, `webui-e2e`; make `build` depend on `webui`)
- Modify: `cmd/node/main.go` (add `--bridge-addr` flag, default `127.0.0.1:0`)

- [ ] **Step 1: Add the dev port flag**

In `cmd/node/main.go`, add `bridgeAddr := flag.String("bridge-addr", "127.0.0.1:0", "loopback bridge bind address")` and pass `*bridgeAddr` to `br.Start(...)` instead of the hard-coded `"127.0.0.1:0"`.

- [ ] **Step 2: Add Makefile targets**

```make
.PHONY: proto test build tidy webui webui-test webui-e2e

webui:
	cd webui && npm ci && npx nuxt generate
	rm -rf internal/bridge/dist
	mkdir -p internal/bridge/dist
	cp -r webui/.output/public/. internal/bridge/dist/

webui-test:
	cd webui && npx vitest run

webui-e2e:
	cd webui && npx playwright test

build: webui
	go build -o bin/node ./cmd/node
	go build -o bin/signal-server ./cmd/signal-server
```

(Keep the existing `proto`, `test`, `tidy` targets.)

- [ ] **Step 3: Verify the full build embeds the real bundle**

Run: `make webui && go build -o bin/node ./cmd/node`
Expected: `internal/bridge/dist/index.html` is the Nuxt build (contains the `<!--__P2P_BOOTSTRAP__-->` marker), and `go build` embeds it. Launch `./bin/node --name Alice --no-open` and `curl -s "$URL/" ` shows the injected `window.__P2P__`.

- [ ] **Step 4: Commit**

```bash
git add Makefile cmd/node/main.go internal/bridge/dist/index.html
git commit -m "build: make webui generates + embeds the Nuxt bundle; --bridge-addr dev flag"
```

---

## Task 10: Playwright smoke (non-blocking)

**Files:**
- Create: `webui/playwright.config.ts`, `webui/e2e/party.spec.ts`, `webui/e2e/harness.ts` (boots two nodes + a signal server)

- [ ] **Step 1: Add Playwright**

Run: `cd webui && npm install -D @playwright/test && npx playwright install chromium`

- [ ] **Step 2: Write the smoke**

`webui/e2e/harness.ts` — start `signal-server`, then two `node` processes on fixed bridge ports (`127.0.0.1:8801`, `:8802`) via `child_process`, parse each printed `UI ready: <url>` line, and return the two URLs (with `?token=`). Tear down in teardown.

`webui/e2e/party.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { startTwoNodes } from './harness'

test('host drives a party; viewer joins and syncs', async ({ browser }) => {
  const { hostURL, viewerURL, hostNodeId, contentId } = await startTwoNodes()
  const host = await browser.newPage(); await host.goto(hostURL)
  const viewer = await browser.newPage(); await viewer.goto(viewerURL)

  // viewer browses host, (auto-allowed test policy), joins the party the host starts
  await host.goto(`${hostURL}/watch/${hostNodeId}/${contentId}?party=1`)
  await viewer.goto(`${viewerURL}/watch/${hostNodeId}/${contentId}`)

  // audience renders 2 watching; viewer shows a Synced indicator
  await expect(viewer.getByText(/Synced/)).toBeVisible({ timeout: 15000 })
  await expect(host.getByText(/2 watching/)).toBeVisible({ timeout: 15000 })
})
```

`webui/playwright.config.ts`: single chromium project, `testDir: 'e2e'`, generous timeouts. The test seeds a shared test fixture file into both nodes' Libraries (reuse the slice-3/4 sample asset path) and uses an open access policy for the test config.

- [ ] **Step 3: Run the smoke locally**

Run: `make webui && make webui-e2e`
Expected: PASS. If flaky on timing, widen timeouts — this job is **non-blocking** by design.

- [ ] **Step 4: Commit**

```bash
git add webui/playwright.config.ts webui/e2e
git commit -m "webui: non-blocking Playwright smoke (two nodes, party sync)"
```

---

## Task 11: CI + final green

**Files:**
- Modify: CI workflow (if present under `.github/workflows/`; otherwise create one) — add a Node setup + `make webui`, run `make webui-test` (blocking) and `go test -race ./...` (blocking); run `make webui-e2e` as a separate non-blocking job.

- [ ] **Step 1: Inspect existing CI**

Run: `ls .github/workflows/ 2>/dev/null || echo "no CI yet"`
If present, add steps; if absent, add a minimal workflow with two jobs (`go` and `webui`) per the spec's Build/Test section. (Match the repo's existing CI conventions if any.)

- [ ] **Step 2: Full local gate**

Run:
```bash
go test -race ./...
make webui-test
make build
```
Expected: all green; binary builds with the embedded UI.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows
git commit -m "ci: build + embed Nuxt UI; vitest + go race blocking, playwright non-blocking"
```

---

## Self-review checklist (done by the plan author)

- **Spec coverage:** Nuxt static SPA + embed (Tasks 2,9) ✓; token bootstrap (3) ✓; REST client (3) ✓; SSE live data (5) ✓; dashboard/browse/watch screens (6,7,8) ✓; host+viewer actuators incl. `driftMs` (1,4,8) ✓; Audience rendering (8,8a) ✓; Vitest pure-logic blocking (3,4,5) ✓; Playwright non-blocking smoke (10) ✓; build/dev/CI wiring (9,11) ✓; "Your Library"/Catalog terminology (6,7) ✓.
- **Gap found & closed during review:** the SSE `audience` event needs a snapshot endpoint to read from — added **Task 8a** (`GET /api/party/audience`). The download button depends on confirming the existing raw-file bridge route — flagged inline in Task 7 to confirm-or-defer, not assumed.
- **Placeholders:** the `// see note` in Task 8 (solo-vs-host role) and the download caveat in Task 7 are decisions spelled out for implementation time, not missing requirements. No `TODO`/`TBD` requirements remain.
- **Type consistency:** the REST shapes match Slice 6a's `SelfView`/`PeerView`/`TitleView`; the player WS `playerMsg`/`Action` JSON matches `internal/app/party.go` after Task 1's lowercase tags (`play/seek/seekMs/rate/driftMs`, `type/role/posMs/playing`); `ViewerAction`/`HostMessage`/`Actuation` are defined in Task 4 and consumed unchanged in Task 8.
- **Cross-plan dependency:** Task 1 + Task 8a are small Go changes that belong with the UI (they define the wire the browser consumes); everything else builds on Slice 6a's merged control plane.
