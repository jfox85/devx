import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

const backendOrigin = process.env.DEVX_WEB_ORIGIN ?? 'http://localhost:7777'

// Extra hostnames (e.g. CF tunnel domains) allowed to reach the dev server,
// supplied as a comma-separated DEVX_WEB_ALLOWED_HOSTS env var. The Go server
// (option B, built dist) does not use this; it only matters when running the
// vite dev server behind Caddy/CF for hot-reload development.
const extraAllowedHosts = process.env.DEVX_WEB_ALLOWED_HOSTS?.split(',')
  .map((h) => h.trim())
  .filter(Boolean) ?? []

// https://vite.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
  },
  server: {
    host: '0.0.0.0',
    allowedHosts: ['localhost', '127.0.0.1', '.localhost', ...extraAllowedHosts],
    proxy: {
      '/api': backendOrigin,
      // Proxy /terminal/* to the backend, with WebSocket support for ttyd.
      '/terminal': {
        target: backendOrigin,
        ws: true,
      },
    },
  },
})
