<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'
  import PaneNav from './PaneNav.svelte'

  export let session
  export let onBack

  let windows = []
  let windowPollTimer

  // Encode session names so slashes ("/") don't split the URL path.
  $: slug = encodeURIComponent(session.name)
  $: iframeURL = `/terminal/${slug}/`

  // Reconnect iframe when session changes (component reused with different session)
  let currentSession = session.name
  $: if (session.name !== currentSession) {
    currentSession = session.name
    windows = []
  }

  async function sendKey(key) {
    // Use tmux send-keys so the key goes to the session's current window/pane,
    // regardless of which client (iframe vs. any other) is in focus.
    try { await apiSendKeys(session.name, key) } catch { /* ignore */ }
  }

  async function switchWindow(index) {
    try { await apiSwitchWindow(session.name, index) } catch { /* ignore */ }
  }

  async function loadWindows() {
    try { windows = await listWindows(session.name) } catch { /* ignore */ }
  }

  onMount(() => {
    loadWindows()
    windowPollTimer = setInterval(loadWindows, 3000)
  })
  onDestroy(() => {
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
  </div>

  <!-- Window nav tabs -->
  <PaneNav {windows} onSwitch={switchWindow} />

  <!-- Terminal iframe -->
  <iframe
    src={iframeURL}
    title="Terminal — {session.name}"
    class="flex-1 min-h-0 w-full border-0"
    allow="clipboard-read; clipboard-write"
  ></iframe>

  <!-- Soft key toolbar -->
  <SoftKeybar onKey={sendKey} />
</div>
