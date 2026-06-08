import { defineConfig } from 'vitest/config'

// Unit tests live in test/. The e2e/ dir holds Playwright specs (run via
// `npx playwright test`), which must NOT be collected by vitest.
export default defineConfig({
  test: {
    environment: 'happy-dom',
    include: ['test/**/*.{test,spec}.ts'],
  },
})
