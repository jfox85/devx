<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys, refreshTerminal } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'

  export let session
  export let onBack

  let windows = []
  let windowPollTimer

  // Encode session names so slashes ("/") don't split the URL path.
  $: slug = encodeURIComponent(session.name)
  $: iframeURL = `/terminal/${slug}/`

  // Reset windows when session changes (component reused with different session)
  let currentSession = session.name
  $: if (session.name !== currentSession) {
    currentSession = session.name
    windows = []
  }

  // When the iframe finishes loading, give ttyd ~800ms to connect and negotiate
  // terminal size, then trigger a tmux resize-window -A. This sends SIGWINCH to
  // all pane processes and forces a full redraw — fixes blank panes on first open.
  async function handleIframeLoad() {
    await new Promise(r => setTimeout(r, 800))
    try { await refreshTerminal(session.name) } catch { /* ignore */ }
  }

  async function sendKey(key) {
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
  <!-- Combined header: back button + window tabs (or session name if no tabs) -->
  <div class="flex items-stretch bg-gray-900 border-b border-gray-800 flex-shrink-0 min-h-[44px]">
    <button on:click={onBack}
      class="px-3 text-gray-400 hover:text-white text-sm flex-shrink-0 border-r border-gray-800 flex items-center">
      ← Back
    </button>
    {#if windows.length > 0}
      <div class="flex items-center gap-1 px-2 overflow-x-auto flex-1">
        {#each windows as win}
          <button on:click={() => switchWindow(win.index)}
            class="text-xs py-1 px-3 rounded flex-shrink-0 whitespace-nowrap transition-colors
                   {win.active ? 'bg-blue-700 text-white' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}">
            {win.index}: {win.name}
          </button>
        {/each}
      </div>
    {:else}
      <span class="flex-1 flex items-center text-white font-medium text-sm truncate px-3">
        {session.name}
      </span>
    {/if}
  </div>

  <!-- Terminal iframe -->
  <iframe
    src={iframeURL}
    title="Terminal — {session.name}"
    class="flex-1 min-h-0 w-full border-0"
    allow="clipboard-read; clipboard-write"
    on:load={handleIframeLoad}
  ></iframe>

  <!-- Soft key toolbar -->
  <SoftKeybar onKey={sendKey} />
</div>
