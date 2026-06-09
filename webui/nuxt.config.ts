// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  ssr: false,
  modules: ['@nuxt/ui'],
  css: ['~/assets/css/main.css'],
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },
  // Cinematic media UI defaults to dark; users can toggle to light.
  colorMode: { preference: 'dark', fallback: 'dark' },
  // Dev: proxy the Go control plane (run `node` with --bridge-addr 127.0.0.1:8787, Task 9).
  nitro: {
    devProxy: {
      '/api': { target: 'http://127.0.0.1:8787', changeOrigin: true, ws: false },
      '/s': { target: 'http://127.0.0.1:8787', changeOrigin: true },
      '/party': { target: 'http://127.0.0.1:8787', changeOrigin: true, ws: true },
    },
  },
})
