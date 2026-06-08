import { expect, test } from '@playwright/test'
import { type Cluster, startCluster } from './harness'

let cluster: Cluster

test.beforeAll(async () => {
  cluster = await startCluster()
})
test.afterAll(async () => {
  await cluster?.teardown()
})

// This is the reliable, non-blocking integration smoke. It boots a real
// signal-server + two real Go nodes and drives the browser party flow up to and
// including the cross-node Join button rendering. That alone exercises the full
// stack: bridge bootstrap injection, the host's library + StartParty, two nodes
// discovering each other over the signal-server, a real WebRTC catalog browse
// from the viewer, and party-live state propagating across the mesh into the
// viewer's UI as a Join button. The actual viewer->host JoinParty navigation and
// sync is covered by the second test below.
test('host starts a party; viewer browses it cross-node and sees Join', async ({ browser }) => {
  // --- Host: load dashboard, start a party from the library title ---
  // Loading the bridge URL injects window.__P2P__ (token + nodeId), so the
  // dashboard reports the host's own Library and bridge.nodeId is the host's id.
  const hostCtx = await browser.newContext()
  const hostPage = await hostCtx.newPage()
  await hostPage.goto(cluster.hostURL)

  // The sample clip must be listed in the host's Library.
  await expect(hostPage.getByText('clip')).toBeVisible()

  await hostPage.getByRole('button', { name: /start party/i }).first().click()
  // startParty -> navigateTo(/watch/{hostId}/{cid}?party=1): host role, AudienceStrip.
  await expect(hostPage).toHaveURL(/\/watch\//)
  // The host's Audience holds remote VIEWERS only (the host is never its own
  // member), so with no viewer joined yet it renders "0 watching".
  await expect(hostPage.getByText(/0 watching/)).toBeVisible()

  // Returning to the dashboard, the "Now watching" panel reflects the live party
  // (server-side party state survives navigating the browser away from /watch).
  await hostPage.goto(cluster.hostURL)
  await expect(hostPage.getByText(/Hosting/)).toBeVisible()
  await expect(hostPage.getByRole('button', { name: /resume/i })).toBeVisible()

  // --- Viewer: load its own bridge (token injected), then open the host's peer page ---
  // We navigate to the peer page only AFTER the party is live: the peer page loads
  // the catalog once on mount, and the Join button is gated on partyLive.
  const viewerBase = new URL(cluster.viewerURL).origin
  const viewerCtx = await browser.newContext()
  const viewerPage = await viewerCtx.newPage()
  await viewerPage.goto(cluster.viewerURL) // dashboard: injects window.__P2P__ (token)
  // Give the two nodes a moment to discover each other so the P2P catalog browse
  // (a real WebRTC round-trip) succeeds on first load.
  await viewerPage.waitForTimeout(2_000)
  await viewerPage.goto(`${viewerBase}/peer/${cluster.hostNodeId}`)

  // The clip must show up in the host's catalog (real cross-node P2P browse) and,
  // because the host's party is live, be annotated as a live party with a Join
  // button. Seeing the Join button proves partyLive propagated across the mesh.
  await expect(viewerPage.getByText('clip')).toBeVisible({ timeout: 30_000 })
  await expect(viewerPage.getByText(/Party/)).toBeVisible({ timeout: 30_000 })
  await expect(viewerPage.getByRole('button', { name: /join/i }).first())
    .toBeVisible({ timeout: 30_000 })
})

// Full viewer->host join + sync. The bridge's bodiless HTTP 200/202 responses
// (e.g. /api/party/join in internal/bridge/api.go) are now handled by the SPA
// client (webui/app/composables/useBridge.ts reads the body as text and only
// JSON.parses when non-empty), so joinParty() resolves and the viewer navigates
// to the watch page. This drives the full cross-node sync: host starts a party,
// the viewer browses it over WebRTC, joins, reaches /watch/ as a viewer (Synced),
// and the host's AudienceStrip reflects the one remote viewer ("1 watching").
test('viewer joins the party and syncs cross-node', async ({ browser }) => {
  const viewerBase = new URL(cluster.viewerURL).origin
  const hostCtx = await browser.newContext()
  const hostPage = await hostCtx.newPage()
  await hostPage.goto(cluster.hostURL)
  await hostPage.getByRole('button', { name: /start party/i }).first().click()
  // startParty -> navigateTo(/watch/{hostId}/{cid}?party=1): host role, AudienceStrip.
  await expect(hostPage).toHaveURL(/\/watch\//)
  // No viewer has joined yet: the host is never its own audience member.
  await expect(hostPage.getByText(/0 watching/)).toBeVisible()

  const viewerCtx = await browser.newContext()
  const viewerPage = await viewerCtx.newPage()
  await viewerPage.goto(cluster.viewerURL)
  await viewerPage.waitForTimeout(2_000)
  await viewerPage.goto(`${viewerBase}/peer/${cluster.hostNodeId}`)
  await viewerPage.getByRole('button', { name: /join/i }).first().click({ timeout: 30_000 })

  // joinParty now resolves -> navigateTo(/watch/{hostId}/{cid}): viewer role; "Synced".
  await expect(viewerPage).toHaveURL(/\/watch\//, { timeout: 30_000 })
  await expect(viewerPage.getByText(/Synced/)).toBeVisible({ timeout: 30_000 })
  // 1 remote viewer joined => the host's AudienceStrip shows "1 watching".
  await expect(hostPage.getByText(/1 watching/)).toBeVisible({ timeout: 30_000 })
})
