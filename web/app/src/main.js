import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    // Version the SW URL and bypass the HTTP cache so Cloudflare/Chrome don't
    // keep an older service worker around when PWA install metadata changes.
    navigator.serviceWorker.register('/sw.js?v=2', { updateViaCache: 'none' }).catch(() => {})
  })
}

const app = mount(App, {
  target: document.getElementById('app'),
})

export default app
