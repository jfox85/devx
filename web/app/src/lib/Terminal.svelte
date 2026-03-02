<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys, refreshTerminal } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'

  export let session
  export let onBack

  let windows = []
  let windowPollTimer
  let iframeEl

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
  // terminal size, then trigger a tmux resize-window -A to fix blank panes.
  async function handleIframeLoad() {
    await new Promise(r => setTimeout(r, 800))
    try { await refreshTerminal(session.name) } catch { /* ignore */ }
  }

  async function sendKey(key) {
    try { await apiSendKeys(session.name, key) } catch { /* ignore */ }
  }

  async function switchWindow(index) {
    try {
      await apiSwitchWindow(session.name, index)
      // Move focus into the terminal iframe so keyboard input goes there
      iframeEl?.focus()
    } catch { /* ignore */ }
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

<!-- Fill parent container (flex-1 set by parent) -->
<div class="flex flex-col flex-1 min-h-0 bg-black">

  <!-- Header: back button + window tabs (or session name if no tabs) -->
  <div class="flex items-stretch bg-[#0a0e1a] border-b border-[#1e2d4a] flex-shrink-0 h-9">
    <button
      on:click={onBack}
      class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono flex-shrink-0 border-r border-[#1e2d4a] flex items-center transition-colors"
      title="back to session list"
    >←</button>

    {#if windows.length > 0}
      <div class="flex items-center gap-1 px-2 overflow-x-auto flex-1">
        {#each windows as win}
          <button
            on:click={() => switchWindow(win.index)}
            class="
              text-[11px] font-mono py-1 px-2.5 flex-shrink-0 whitespace-nowrap transition-colors
              {win.active
                ? 'text-cyan-300 bg-cyan-950/50 border-b-2 border-cyan-500'
                : 'text-gray-600 hover:text-gray-300 border-b-2 border-transparent'}
            "
          >{win.index}:{win.name}</button>
        {/each}
      </div>
    {:else}
      <span class="flex-1 flex items-center text-gray-500 font-mono text-xs truncate px-3">
        {session.name}
      </span>
    {/if}
  </div>

  <!-- Terminal iframe -->
  <iframe
    bind:this={iframeEl}
    src={iframeURL}
    title="Terminal — {session.name}"
    class="flex-1 min-h-0 w-full border-0"
    allow="clipboard-read; clipboard-write"
    on:load={handleIframeLoad}
  ></iframe>

  <!-- Soft key toolbar — mobile only -->
  <div class="lg:hidden">
    <SoftKeybar onKey={sendKey} />
  </div>

</div>
