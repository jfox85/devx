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
  // terminal size, then refresh and focus.
  async function handleIframeLoad() {
    await new Promise(r => setTimeout(r, 800))
    // Focus first so keyboard input goes to the terminal immediately
    iframeEl?.focus()
    try { await refreshTerminal(session.name) } catch { /* ignore */ }
  }

  async function sendKey(key) {
    try { await apiSendKeys(session.name, key) } catch { /* ignore */ }
  }

  async function switchWindow(index) {
    // Focus synchronously while still inside the click user-gesture context.
    // Browsers won't honour .focus() after an await (outside the gesture).
    iframeEl?.focus()
    try { await apiSwitchWindow(session.name, index) } catch { /* ignore */ }
  }

  async function loadWindows() {
    try { windows = await listWindows(session.name) } catch { /* ignore */ }
  }

  // Tell the session list sidebar to focus its search input so the user can
  // quickly switch sessions from the keyboard.
  function focusSessionList() {
    window.dispatchEvent(new CustomEvent('devx:focusSessionList'))
  }

  onMount(() => {
    loadWindows()
    windowPollTimer = setInterval(loadWindows, 3000)
  })
  onDestroy(() => {
    clearInterval(windowPollTimer)
  })
</script>

<!-- Fill parent container (flex-1 set by App.svelte) -->
<div class="flex flex-col flex-1 min-h-0 bg-black">

  <!-- Header: back + session-list shortcut + window tabs -->
  <div class="flex items-stretch bg-[#0a0e1a] border-b border-[#1e2d4a] flex-shrink-0 h-9">
    <!-- Back / deselect -->
    <button
      on:click={onBack}
      class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono flex-shrink-0 border-r border-[#1e2d4a] flex items-center transition-colors"
      title="back to session list"
    >←</button>

    <!-- Window tabs (or session name if no tabs) -->
    {#if windows.length > 0}
      <div class="flex items-center gap-1 px-2 overflow-x-auto flex-1 min-w-0">
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
      <span class="flex-1 flex items-center text-gray-500 font-mono text-xs truncate px-3 min-w-0">
        {session.name}
      </span>
    {/if}

    <!-- "/ sessions" button: desktop only — focuses the sidebar search for fast switching -->
    <button
      on:click={focusSessionList}
      class="hidden lg:flex items-center px-3 text-[10px] font-mono text-gray-700 hover:text-cyan-400 border-l border-[#1e2d4a] flex-shrink-0 transition-colors"
      title="focus session list (press / to search, ↑↓ to navigate, ⏎ to open)"
    >/ sessions</button>
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
