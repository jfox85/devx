<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listWindows } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'
  import PaneNav from './PaneNav.svelte'

  export let session
  export let onBack

  let ws
  let wsReady = false
  let error = ''
  let windows = []
  let windowPollTimer

  // Encode session names so slashes ("/") don't split the URL path.
  // The server parses %2F from RawPath for the initial request, and uses
  // prefix-matching on active sessions for subsequent asset requests.
  $: slug = encodeURIComponent(session.name)
  $: iframeURL = `/terminal/${slug}/`

  function connectWS() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${proto}://${location.host}/terminal/${slug}/ws`, ['tty'])
    ws.onopen = () => { wsReady = true }
    ws.onerror = () => { error = 'Terminal connection failed' }
    ws.onclose = () => { wsReady = false }
  }

  // Reconnect when the session changes (component reused with a different session)
  let currentSession = session.name

  $: if (session.name !== currentSession) {
    currentSession = session.name
    if (ws) ws.close()
    error = ''
    wsReady = false
    connectWS()
  }

  function sendKey(seq) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send('0' + seq)
    }
  }

  function switchWindow(index) {
    // Ctrl-B (tmux prefix) + window number
    sendKey('\x02' + String(index))
  }

  async function loadWindows() {
    try { windows = await listWindows(session.name) } catch { /* ignore */ }
  }

  onMount(() => {
    connectWS()
    loadWindows()
    windowPollTimer = setInterval(loadWindows, 3000)
  })
  onDestroy(() => {
    if (ws) ws.close()
    clearInterval(windowPollTimer)
  })
</script>

<div class="fixed inset-0 flex flex-col bg-black">
  <!-- Header bar -->
  <div class="flex items-center gap-3 px-3 py-2 bg-gray-900 border-b border-gray-800 flex-shrink-0">
    <button on:click={onBack}
      class="text-gray-400 hover:text-white text-sm px-2 py-1 rounded transition-colors">
      ← Back
    </button>
    <span class="text-white font-medium text-sm truncate flex-1 min-w-0">{session.name}</span>
    <div class="flex items-center gap-2 flex-shrink-0">
      <div class="w-2 h-2 rounded-full {wsReady ? 'bg-green-500' : 'bg-red-500'}"
           title={wsReady ? 'Connected' : 'Disconnected'}></div>
      {#if !wsReady}
        <button on:click={() => { error = ''; connectWS() }}
          class="text-xs text-gray-400 hover:text-white px-1">
          ↺
        </button>
      {/if}
    </div>
  </div>

  <!-- Window nav tabs -->
  <PaneNav {windows} onSwitch={switchWindow} />

  <!-- Terminal iframe -->
  {#if error}
    <div class="flex-1 flex items-center justify-center text-red-400 text-sm p-4 text-center">{error}</div>
  {:else}
    <iframe
      src={iframeURL}
      title="Terminal — {session.name}"
      class="flex-1 min-h-0 w-full border-0"
      allow="clipboard-read; clipboard-write"
    ></iframe>
  {/if}

  <!-- Soft key toolbar -->
  <SoftKeybar onKey={sendKey} disabled={!wsReady} />
</div>
