import { defineConfig } from 'vitest/config'

// Standalone vitest config. The Svelte vite-plugin's HMR hooks are not
// compatible with the vitest server lifecycle, and these tests only exercise
// plain ES modules (no component mounting), so we omit it here.
export default defineConfig({
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.js'],
  },
})
