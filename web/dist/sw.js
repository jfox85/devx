self.addEventListener('install', (event) => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

self.addEventListener('fetch', (event) => {
  // Network-first/no-op service worker. Explicitly responding with fetch keeps
  // browsers that require a functional fetch handler happy for installability,
  // while avoiding caches so auth, ttyd websockets, SSE, and live APIs stay fresh.
  event.respondWith(fetch(event.request))
})
