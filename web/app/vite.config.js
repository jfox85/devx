import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

const backendOrigin = process.env.DEVX_WEB_ORIGIN ?? 'http://localhost:7777'

// https://vite.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
  },
  server: {
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
