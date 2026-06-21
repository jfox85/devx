import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { isDesktop } from './lib/desktopBridge.js'

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    if (isDesktop()) {
      // The desktop shell serves the SPA through Wails' asset server + private
      // proxy. A PWA service worker is unnecessary there and can leave stale
      // wails:// asset responses across app rebuilds, causing broken/mismatched
      // UI. Clean up any worker a previous desktop build registered.
      navigator.serviceWorker.getRegistrations()
        .then(regs => regs.forEach(reg => reg.unregister()))
        .catch(() => {})
      return
    }
    // Version the SW URL and bypass the HTTP cache so Cloudflare/Chrome don't
    // keep an older service worker around when PWA install metadata changes.
    navigator.serviceWorker.register('/sw.js?v=2', { updateViaCache: 'none' }).catch(() => {})
  })
}

const app = mount(App, {
  target: document.getElementById('app'),
})

export default app
