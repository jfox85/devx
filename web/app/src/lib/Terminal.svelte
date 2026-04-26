<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { getActivePane, listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys, sendLiteral, refreshTerminal, uploadImage } from '../api.js'
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
  const ALLOWED_EXTS = ['.png', '.jpg', '.jpeg', '.gif', '.webp']
  function isImageFile(f) {
    if (ALLOWED_TYPES.includes(f.type)) return true
    // Fallback: check extension when MIME type is missing or generic (e.g. macOS Finder)
    const name = (f.name || '').toLowerCase()
    return ALLOWED_EXTS.some(ext => name.endsWith(ext))
  }

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

  async function openPaneViewer() {
    // Open synchronously while still inside the click gesture context so
    // mobile browsers do not treat it as a blocked popup after awaiting.
    const viewerWindow = window.open('', '_blank')
    const params = new URLSearchParams({ name: session.name })
    try {
      const pane = await getActivePane(session.name)
      if (pane != null && !Number.isNaN(Number(pane))) params.set('pane', String(pane))
    } catch {
      // Fall back to tmux's current active pane if the lookup fails.
    }
    const url = `/api/pane-content/view?${params.toString()}`
    if (viewerWindow) {
      viewerWindow.location.replace(url)
    } else {
      window.open(url, '_blank', 'noopener,noreferrer')
    }
  }

  // When the iframe finishes loading, wait for xterm.js to fully initialise
  // (indicated by the helper textarea appearing), then:
  //   1. Call term.fit() → FitAddon re-measures the element and sends the
  //      correct cols/rows to the PTY via WebSocket (ioctl TIOCSWINSZ →
  //      SIGWINCH → shell redraws).
  //   2. Call refreshTerminal which does refresh-client (forces display
  //      redraw) and resize-window to the current client's dimensions,
  //      working around the tmux grouped-session size-constraint bug.
  async function handleIframeLoad() {
    // Inject Nerd Font into the iframe immediately so the font is available
    // before xterm.js initialises and measures character cell size.
    // The font file is already cached by the parent page's preload hint.
    try {
      const link = iframeEl.contentDocument.createElement('link')
      link.rel = 'stylesheet'
      link.href = '/nerd-font.css'
      iframeEl.contentDocument.head.appendChild(link)

      const touchStyle = iframeEl.contentDocument.createElement('style')
      touchStyle.textContent = `
        html, body {
          height: 100%;
          margin: 0;
          overflow: hidden;
          overscroll-behavior: none;
        }
        .xterm, .xterm-viewport {
          touch-action: pan-y !important;
        }
        .xterm-viewport {
          -webkit-overflow-scrolling: touch !important;
          overscroll-behavior-y: contain;
        }
      `
      iframeEl.contentDocument.head.appendChild(touchStyle)
      // Wait for the font to be ready before xterm starts measuring.
      await iframeEl.contentWindow.document.fonts.load('12px HackNerdFontMono')
    } catch { /* ignore cross-origin / not-yet-loaded */ }

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
    // Restore the previously-selected window for this session.
    await restoreStoredWindow()
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
        const files = Array.from(e.dataTransfer?.files || [])
        if (files.length) processImageFiles(files)
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

  // Stable sessionStorage key for the active window preference of a session.
  const windowStorageKey = (name) => 'devx_activeWindow_' + name

  // Persist the active window whenever the windows list changes — covers the
  // case where the terminal is already on the right window without the user
  // ever clicking a tab (e.g. tmux state was already set). This ensures
  // restoreStoredWindow always has a value to work with.
  $: {
    const activeWin = windows.find(w => w.active)
    if (activeWin && session?.name) {
      sessionStorage.setItem(windowStorageKey(session.name), String(activeWin.index))
    }
  }

  async function switchWindow(index) {
    // Must focus synchronously while still in the click user-gesture context.
    // After an await, browsers may ignore .focus() calls.
    focusTerminal()
    const name = session.name
    sessionStorage.setItem(windowStorageKey(name), String(index))
    // Optimistic update: highlight the clicked tab immediately so the user
    // gets instant feedback without waiting for the next poll cycle.
    windows = windows.map(w => ({ ...w, active: w.index === index }))
    try { await apiSwitchWindow(name, index) } catch { /* ignore */ }
  }

  // Restore the last-selected window for this session. Called after the iframe
  // loads so that switching sessions and back doesn't reset the active pane.
  async function restoreStoredWindow() {
    // Capture session name before any await so a mid-flight session change
    // doesn't cause us to operate on the wrong session.
    const name = session.name
    const stored = sessionStorage.getItem(windowStorageKey(name))
    if (stored === null) return
    const storedIndex = parseInt(stored, 10)
    if (isNaN(storedIndex)) return
    try {
      const wins = await listWindows(name)
      // Bail if the session changed while we were awaiting.
      if (session.name !== name) return
      const target = wins.find(w => w.index === storedIndex)
      if (target && !target.active) {
        await apiSwitchWindow(name, storedIndex)
        if (session.name !== name) return
        // Update local state optimistically — no second round-trip needed.
        windows = wins.map(w => ({ ...w, active: w.index === storedIndex }))
      } else if (wins.length > 0) {
        windows = wins
      }
    } catch { /* ignore */ }
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

  // Core image upload and path injection logic. Accepts one or more files,
  // uploads them in parallel, and injects all paths space-separated into the
  // active tmux pane.
  async function processImageFiles(files) {
    if (!files.length || uploading) return

    const valid = files.filter(isImageFile)
    if (valid.length === 0) {
      toastUpload = null
      toastError = `Unsupported type: ${files[0].type || files[0].name || 'unknown'}`
      return
    }

    uploading = true
    const objectURLs = valid.map(f => URL.createObjectURL(f))

    try {
      const results = await Promise.all(valid.map(f => uploadImage(f)))
      const paths = results.map(r => r.path)
      // Inject all paths into active tmux pane (no Enter — user confirms).
      // Use sendLiteral so spaces in paths are preserved verbatim.
      await sendLiteral(session.name, paths.join(' ') + ' ')
      toastError = null
      toastUpload = {
        path: paths.length === 1 ? paths[0] : `${paths.length} images uploaded`,
        objectURL: objectURLs[0],
      }
      // Revoke extra objectURLs not used by the toast preview.
      objectURLs.slice(1).forEach(u => URL.revokeObjectURL(u))
    } catch (e) {
      objectURLs.forEach(u => URL.revokeObjectURL(u))
      toastUpload = null
      toastError = e.message || 'Upload failed'
    } finally {
      uploading = false
    }
  }

  // Single-file convenience wrapper (paste handlers).
  function processImageFile(file) {
    if (file) processImageFiles([file])
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
    const files = Array.from(e.target.files || [])
    if (files.length) processImageFiles(files)
    // Reset so the same file(s) can be selected again
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
    const files = Array.from(e.dataTransfer?.files || [])
    if (files.length) processImageFiles(files)
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

    <button
      on:click={openPaneViewer}
      title="open full pane output in a new tab"
      class="px-3 text-gray-500 hover:text-cyan-300 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center justify-center transition-colors min-w-[58px]"
    >[view]</button>

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
      multiple
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
