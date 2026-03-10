<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys, refreshTerminal, uploadImage } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'
  import ImageToast from './ImageToast.svelte'

  export let session
  export let onBack

  let windows = []
  let windowPollTimer
  let iframeEl
  let fileInputEl

  // Drag-and-drop state
  let isDragOver = false
  let dragCounter = 0

  // Toast state
  let toastUpload = null  // { path, objectURL } | null
  let toastError = null   // string | null
  let uploading = false   // guard against concurrent uploads

  const ALLOWED_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

  // Encode session names so slashes ("/") don't split the URL path.
  $: slug = encodeURIComponent(session.name)
  $: iframeURL = `/terminal/${slug}/`

  // iframeKey drives the {#key} block around the iframe. Changing it destroys
  // and recreates the iframe element, causing xterm.js to reconnect and
  // re-report the correct terminal dimensions to the server.
  //
  // We bump it in two situations:
  //   1. Session changes — same as the old {#key session.name} behaviour.
  //   2. Tab was hidden for > 30 s — handles the case where another device
  //      (e.g. phone) resized the PTY while this tab was in the background.
  //      On return, xterm.js reconnects and immediately sends the correct
  //      dimensions, so the terminal reflowes to this viewport.
  let iframeKey = session.name
  let hiddenAt = null

  // Reset windows and iframe key when session changes (component reused with
  // different session). currentSession stores the previous value of session.name
  // so the reactive block can detect the transition — Svelte reactive statements
  // don't receive the old value, so we track it manually.
  let currentSession = session.name
  $: if (session.name !== currentSession) {
    currentSession = session.name
    windows = []
    iframeKey = session.name
  }

  // Trigger ttyd's FitAddon to re-measure the terminal element and send the
  // correct cols/rows to the PTY via WebSocket. ttyd exposes
  // window.term.fit = () => fitAddon.fit() on the same origin; calling it
  // directly is required because ttyd's FitAddon uses ResizeObserver
  // internally and does not listen to window resize events.
  function triggerFitAddon() {
    try {
      iframeEl?.contentWindow?.term?.fit?.()
    } catch { /* ignore cross-origin / not-yet-loaded */ }
  }

  function handleVisibilityChange() {
    if (document.hidden) {
      hiddenAt = Date.now()
      return
    }
    if (hiddenAt !== null) {
      const absent = Date.now() - hiddenAt
      hiddenAt = null
      if (absent > 180_000) {
        // Long absence: reload iframe entirely so xterm.js reconnects fresh.
        iframeKey = session.name + '::' + Date.now()
      } else {
        // Short absence: another device may have changed the PTY size while
        // this tab was backgrounded. Re-trigger FitAddon so xterm.js sends
        // the current viewport's correct cols/rows to the PTY via ioctl.
        triggerFitAddon()
        scheduleRefresh()
      }
    }
  }

  // Focus the terminal inside the iframe.
  //
  // iframeEl.focus() routes keyboard events to the iframe's window, but
  // xterm.js captures input through its own textarea (.xterm-helper-textarea).
  // We need to focus that element directly; otherwise typing after a programmatic
  // focus still requires a manual click.
  //
  // This is same-origin (ttyd is served by the same server) so contentDocument
  // access is allowed.
  function focusTerminal() {
    try {
      const textarea = iframeEl?.contentDocument?.querySelector('.xterm-helper-textarea')
      if (textarea) {
        textarea.focus()
        return
      }
    } catch { /* ignore any cross-origin / not-yet-loaded errors */ }
    // Fallback: at minimum route events to the iframe window
    iframeEl?.focus()
  }

  // Ctrl+Shift+S, registered on the iframe's document in capture phase so
  // xterm never sees it. Dispatches to the parent window (lexical `window`
  // is the parent since this function is defined in the parent scope).
  function iframeHotkey(e) {
    if (e.ctrlKey && e.shiftKey && (e.key === 's' || e.key === 'S')) {
      e.preventDefault()
      e.stopPropagation()
      window.dispatchEvent(new CustomEvent('devx:focusSessionList'))
    } else if (e.ctrlKey && e.shiftKey && (e.key === 'c' || e.key === 'C')) {
      e.preventDefault()
      e.stopPropagation()
      window.dispatchEvent(new CustomEvent('devx:newSession'))
    }
  }

  // Intercept paste events inside the iframe to capture image pastes.
  function iframePaste(e) {
    const items = e.clipboardData?.items || []
    for (const item of items) {
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        e.preventDefault()
        e.stopPropagation()
        processImageFile(item.getAsFile())
        return
      }
    }
    // No image found — let text paste proceed normally
  }

  // Timing constants for xterm.js / FitAddon initialisation.
  const XTERM_POLL_DEADLINE_MS = 5000  // max time to wait for xterm.js init
  const XTERM_POLL_INTERVAL_MS = 100   // polling interval while waiting
  const FITADDON_SETTLE_MS     = 200   // time for FitAddon → ioctl to propagate

  // When the iframe finishes loading, wait for xterm.js to fully initialise
  // (indicated by the helper textarea appearing), then:
  //   1. Call term.fit() → FitAddon re-measures the element and sends the
  //      correct cols/rows to the PTY via WebSocket (ioctl TIOCSWINSZ →
  //      SIGWINCH → shell redraws).
  //   2. Call refreshTerminal which does refresh-client (forces display
  //      redraw) and resize-window to the current client's dimensions,
  //      working around the tmux grouped-session size-constraint bug.
  async function handleIframeLoad() {
    // Poll until xterm's helper textarea appears (signals full init).
    const deadline = Date.now() + XTERM_POLL_DEADLINE_MS
    while (Date.now() < deadline) {
      try {
        if (iframeEl?.contentDocument?.querySelector('.xterm-helper-textarea')) break
      } catch { /* cross-origin / not-yet-loaded */ }
      await new Promise(r => setTimeout(r, XTERM_POLL_INTERVAL_MS))
    }
    // Re-trigger FitAddon so it sends the current browser viewport dimensions
    // to the PTY. Small wait after so ioctl has time to propagate before the
    // subsequent refresh-client call.
    triggerFitAddon()
    await new Promise(r => setTimeout(r, FITADDON_SETTLE_MS))
    try { await refreshTerminal(session.name) } catch { /* ignore */ }
    focusTerminal()
    // Register the hotkey after focus so xterm is initialised
    try {
      iframeEl.contentDocument?.addEventListener('keydown', iframeHotkey, { capture: true })
      iframeEl.contentDocument?.addEventListener('paste', iframePaste, { capture: true })
      // Drag events do not bubble across iframe boundaries, so a file dragged
      // over the iframe never reaches the outer div's dragenter/drop handlers.
      // Mirror the events onto the parent window so the drop overlay appears
      // and the file is processed correctly.
      iframeEl.contentDocument?.addEventListener('dragenter', (e) => {
        const hasFiles = Array.from(e.dataTransfer?.items || []).some(i => i.kind === 'file')
        if (hasFiles) { dragCounter++; isDragOver = true }
      })
      iframeEl.contentDocument?.addEventListener('dragleave', () => {
        dragCounter--
        if (dragCounter <= 0) { dragCounter = 0; isDragOver = false }
      })
      iframeEl.contentDocument?.addEventListener('dragover', (e) => e.preventDefault())
      iframeEl.contentDocument?.addEventListener('drop', (e) => {
        e.preventDefault()
        dragCounter = 0; isDragOver = false
        const file = e.dataTransfer?.files?.[0]
        if (file) processImageFile(file)
      })
    } catch { /* ignore if contentDocument isn't accessible yet */ }
    // Watch for iframe size changes (mobile browser chrome, keyboard, orientation)
    resizeObserver?.disconnect()
    resizeObserver = new ResizeObserver(scheduleRefresh)
    resizeObserver.observe(iframeEl)
  }

  async function sendKey(key) {
    try { await apiSendKeys(session.name, key) } catch { /* ignore */ }
  }

  async function switchWindow(index) {
    // Must focus synchronously while still in the click user-gesture context.
    // After an await, browsers may ignore .focus() calls.
    focusTerminal()
    try { await apiSwitchWindow(session.name, index) } catch { /* ignore */ }
  }

  async function loadWindows() {
    try { windows = await listWindows(session.name) } catch { /* ignore */ }
  }

  // Debounced resize handler: fires when the iframe changes size (mobile
  // browser chrome, soft keyboard, orientation, window resize).
  // Calls term.fit() so FitAddon re-measures and sends the correct dims to
  // the PTY, then follows up with refreshTerminal (refresh-client +
  // resize-window) for a full tmux sync.
  let resizeTimer
  let resizeObserver
  function scheduleRefresh() {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(async () => {
      triggerFitAddon()
      await new Promise(r => setTimeout(r, FITADDON_SETTLE_MS))
      try { await refreshTerminal(session.name) } catch { /* ignore */ }
    }, 300)
  }

  // Core image upload and path injection logic.
  async function processImageFile(file) {
    if (!file || uploading) return

    // Fast client-side MIME check before uploading
    if (!ALLOWED_TYPES.includes(file.type)) {
      toastUpload = null
      toastError = `Unsupported type: ${file.type || 'unknown'}`
      return
    }

    uploading = true
    const objectURL = URL.createObjectURL(file)

    try {
      const result = await uploadImage(file)
      const path = result.path
      // Inject path into active tmux pane (no Enter — user confirms)
      await apiSendKeys(session.name, path)
      toastError = null
      toastUpload = { path, objectURL }
    } catch (e) {
      URL.revokeObjectURL(objectURL)
      toastUpload = null
      toastError = e.message || 'Upload failed'
    } finally {
      uploading = false
    }
  }

  function dismissToast() {
    if (toastUpload?.objectURL) URL.revokeObjectURL(toastUpload.objectURL)
    toastUpload = null
    toastError = null
  }

  // Exported so App.svelte can route parent-window paste events here.
  export function handleImagePaste(file) {
    processImageFile(file)
  }

  function handleFileInput(e) {
    const file = e.target.files?.[0]
    if (file) processImageFile(file)
    // Reset so the same file can be selected again
    e.target.value = ''
  }

  // Drag-and-drop handlers on the outer div (not the iframe).
  function handleDragEnter(e) {
    const hasFiles = Array.from(e.dataTransfer?.items || []).some(i => i.kind === 'file')
    if (!hasFiles) return
    dragCounter++
    isDragOver = true
  }

  function handleDragLeave() {
    dragCounter--
    if (dragCounter <= 0) {
      dragCounter = 0
      isDragOver = false
    }
  }

  function handleDragOver(e) {
    e.preventDefault()
  }

  function handleDrop(e) {
    e.preventDefault()
    dragCounter = 0
    isDragOver = false
    const file = e.dataTransfer?.files?.[0]
    if (file) processImageFile(file)
  }

  onMount(() => {
    loadWindows()
    windowPollTimer = setInterval(loadWindows, 3000)
    // visualViewport fires on mobile when the address bar hides/shows or the
    // soft keyboard appears — more reliable than ResizeObserver alone.
    window.visualViewport?.addEventListener('resize', scheduleRefresh)
    document.addEventListener('visibilitychange', handleVisibilityChange)
  })
  onDestroy(() => {
    clearInterval(windowPollTimer)
    clearTimeout(resizeTimer)
    resizeObserver?.disconnect()
    window.visualViewport?.removeEventListener('resize', scheduleRefresh)
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    if (toastUpload?.objectURL) URL.revokeObjectURL(toastUpload.objectURL)
  })
</script>

<!-- Fill parent container (flex-1 set by App.svelte) -->
<div
  class="flex flex-col flex-1 min-h-0 bg-black relative"
  role="region"
  aria-label="terminal with image drop target"
  on:dragenter={handleDragEnter}
  on:dragleave={handleDragLeave}
  on:dragover={handleDragOver}
  on:drop={handleDrop}
>

  <!-- Drag-and-drop overlay -->
  {#if isDragOver}
    <div class="absolute inset-0 z-40 bg-cyan-950/60 border-2 border-cyan-500 border-dashed
                flex flex-col items-center justify-center pointer-events-none">
      <div class="text-cyan-400 font-mono text-lg">drop image</div>
      <div class="text-cyan-600 font-mono text-[11px]">png · jpg · gif · webp</div>
    </div>
  {/if}

  <!-- Header: back + window tabs (or session name) + attach button -->
  <div class="flex items-stretch bg-[#0a0e1a] border-b border-[#1e2d4a] shrink-0 h-9">
    <button
      on:click={onBack}
      class="px-3 text-gray-400 hover:text-cyan-400 text-xs font-mono shrink-0 border-r border-[#1e2d4a] flex items-center transition-colors"
      title="back to session list"
    >←</button>

    {#if windows.length > 0}
      <div role="tablist" class="flex items-center gap-1 px-2 overflow-x-auto flex-1 min-w-0">
        {#each windows as win}
          <button
            role="tab"
            aria-selected={win.active}
            on:click={() => switchWindow(win.index)}
            class="
              text-[11px] font-mono py-1 px-2.5 shrink-0 whitespace-nowrap transition-colors
              {win.active
                ? 'text-cyan-300 bg-cyan-950/50 border-b-2 border-cyan-500'
                : 'text-gray-600 hover:text-gray-300 border-b-2 border-transparent'}
            "
          >{win.index}:{win.name}</button>
        {/each}
      </div>
    {:else}
      <button
        on:click={scheduleRefresh}
        title="tap to re-sync terminal size"
        class="flex-1 flex items-center text-gray-500 font-mono text-xs truncate px-3 min-w-0 text-left"
      >{session.name}</button>
    {/if}

    <!-- Attach image button -->
    <button
      on:click={() => fileInputEl?.click()}
      title="attach image"
      class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
    >[img]</button>
    <input
      bind:this={fileInputEl}
      type="file"
      accept="image/png,image/jpeg,image/gif,image/webp"
      class="hidden"
      on:change={handleFileInput}
    />
  </div>

  <!--
    Wrap in {#key} so switching sessions destroys the old iframe element rather
    than navigating it. Navigating triggers ttyd's beforeunload handler and shows
    the browser's "Leave site?" dialog. Removing an iframe element does not.
  -->
  {#key iframeKey}
    <iframe
      bind:this={iframeEl}
      src={iframeURL}
      title="Terminal — {session.name}"
      class="flex-1 min-h-0 w-full border-0"
      allow="clipboard-read; clipboard-write"
      on:load={handleIframeLoad}
    ></iframe>
  {/key}

  <!-- Soft key toolbar — mobile only -->
  <div class="lg:hidden">
    <SoftKeybar onKey={sendKey} />
  </div>

  <!-- Image upload confirmation / error toast -->
  {#if toastUpload || toastError}
    <ImageToast upload={toastUpload} error={toastError} onDismiss={dismissToast} />
  {/if}

</div>
