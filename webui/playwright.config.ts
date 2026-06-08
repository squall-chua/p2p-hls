import { defineConfig, devices } from '@playwright/test'

// Non-blocking smoke: boots a signal-server + two real Go nodes (see e2e/harness.ts)
// and drives the browser party flow. WebRTC/transcode timing makes this inherently
// slower than a unit test, so timeouts are generous and workers is pinned to 1
// (fixed ports 8080/8801/8802, no port races).
export default defineConfig({
  testDir: 'e2e',
  fullyParallel: false,
  workers: 1,
  retries: 1,
  timeout: 60_000,
  expect: { timeout: 30_000 },
  reporter: [['list']],
  use: {
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
})
