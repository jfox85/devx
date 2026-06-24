<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { getActivePane, listWindows, switchWindow as apiSwitchWindow, sendKeys as apiSendKeys, sendLiteral, sendInput, refreshTerminal, uploadImage, listArtifacts, getSettings, clearArtifactFocus } from '../api.js'
  import SoftKeybar from './SoftKeybar.svelte'
  import ImageToast from './ImageToast.svelte'
  import ArtifactPane from './artifacts/ArtifactPane.svelte'
  import MobileActionsMenu from './terminal/MobileActionsMenu.svelte'
  import PaneViewerModal from './terminal/PaneViewerModal.svelte'
  import ArtifactSearchOverlay from './terminal/ArtifactSearchOverlay.svelte'
  import PromptComposer from './composer/PromptComposer.svelte'
  import { getSessionChrome, setSessionChrome, markIframeLoad, markTerminalReady } from './stores/sessionUiState.js'
  import { isImageFile } from './imagePolicy.js'
  import { isDesktop, desktopConfig, clipboardImage, uploadImage as desktopUploadImage, openExternal } from './desktopBridge.js'
  import { attachFrameInputListeners as attachListeners } from './terminal/frameInputListeners.js'

  export let session
  export let artifactEvent = null
  export let onBack

  let windows = []
  let windowPollTimer
  let iframeEl
  let fileInputEl
  let keyboardProxyEl
  let keyboardProxyValue = ''
  let keyboardProxyComposing = false
  let keyboardProxyQueue = Promise.resolve()
  let keyboardProxyTextBuffer = ''
  let keyboardProxyTextSession = ''
  let keyboardProxyFlushTimer

  // Artifact pane/reference state
  let artifactPaneOpen = false
  let artifactFullScreen = false
  let paneViewerOpen = false
  let paneViewerURL = ''
  let actionsMenuOpen = false
  let modalStack = []
  let suppressNextPopState = false
  let splitMode = 'vertical' // vertical | horizontal | artifacts | terminal
  let artifactSearchOpen = false
  let artifactQuery = ''
  let artifactSearchItems = []
  let artifactTriggerKey = 'Ctrl+Space'
  let artifactSearchIndex = 0
  let selectedArtifactID = null
  let lastArtifactEventNonce = null
  let artifactOverlayMode = 'insert' // insert | open
  let focusedArtifactDismissed = false
  let pasteArtifactNonce = 0
  let focusedArtifactTimer
  let composerOpen = false
  let composerComponent = null
  let softKeysOpen = false
  $: terminalIsVisible = !artifactPaneOpen || splitMode !== 'artifacts'
  $: artifactsIsVisible = artifactPaneOpen && splitMode !== 'terminal'
  $: terminalPaneCSS = !artifactsIsVisible ? 'flex: 1 1 0;' : splitMode === 'vertical' ? 'width: 50%; flex: 0 0 50%;' : splitMode === 'horizontal' ? 'height: 50%; flex: 0 0 50%;' : 'flex: 1 1 0;'
  $: artifactPaneCSS = splitMode === 'vertical' ? 'width: 50%; flex: 0 0 50%;' : splitMode === 'horizontal' ? 'height: 50%; flex: 0 0 50%;' : 'flex: 1 1 0;'
  $: filteredArtifactSearchItems = artifactSearchItems.filter(a => {
    const q = artifactQuery.trim().toLowerCase()
    if (!q) return true
    return [a.title, a.file, a.folder, a.type, ...(a.tags || [])].join(' ').toLowerCase().includes(q)
  })

  // Drag-and-drop state
  let isDragOver = false
  let dragCounter = 0

  // Toast state
  let toastUpload = null  // { path, objectURL } | null
  let toastError = null   // string | null
  let uploading = false   // guard against concurrent uploads

  // Reconstruct a File from a base64 payload bridged by the desktop host
  // (file drops and clipboard images). Exported so App.svelte shares one decoder.
  export function fileFromBase64({ name, type, data }) {
    const bin = atob(data)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new File([bytes], name || 'image.png', { type: type || 'image/png' })
  }

  // --- Iframe keep-alive pool (plan 0C) ---------------------------------------
  // Recently used session iframes stay mounted (absolutely positioned, hidden
  // via visibility) so switching back skips iframe teardown, ttyd HTML reload,
  // and the WebSocket re-handshake entirely. visibility:hidden (not
  // display:none) keeps the layout box, so xterm's measured dimensions stay
  // valid and FitAddon has real geometry on re-show. pointer-events:none and
  // tabindex=-1 on hidden frames prevent focus/click stealing.
  //
  // Pool entries: { name, key } — bumping key recreates that session's iframe
  // (used for the long-absence reload path). Active session is always pool[0].
  // Mobile gets no pool (cap 1): keeping multiple xterm WebGL contexts alive
  // on a phone costs memory and background sockets die with the tab anyway.
  const IFRAME_POOL_MAX = (typeof window !== 'undefined' && window.innerWidth >= 1024) ? 3 : 1
  let pool = [{ name: session.name, key: 0 }]
  let frameEls = {}
  // Frames whose xterm never initialised (e.g. backend returned an error page)
  // must not be served from the pool — promote forces a fresh reload instead.
  let frameHealthy = {}
  let hiddenAt = null

  function frameURL(name) {
    // Encode session names so slashes ("/") don't split the URL path.
    const path = `/terminal/${encodeURIComponent(name)}/`
    const desktop = desktopConfig()
    if (!desktop?.terminalBase || !desktop?.terminalToken) return path
    const url = new URL(path, desktop.terminalBase)
    url.searchParams.set('desktop_token', desktop.terminalToken)
    return url.toString()
  }

  function reloadActiveFrame() {
    pool = pool.map(p => p.name === session.name ? { ...p, key: Date.now() } : p)
  }

  // Reset windows and iframe key when session changes (component reused with
  // different session). currentSession stores the previous value of session.name
  // so the reactive block can detect the transition — Svelte reactive statements
  // don't receive the old value, so we track it manually.
  let currentSession = session.name
  $: if (session.name !== currentSession) {
    // Save outgoing session's layout chrome so switching back restores it.
    setSessionChrome(currentSession, { splitMode, artifactPaneOpen, selectedArtifactID })
    currentSession = session.name
    windows = []
    promoteInPool(session.name)
    artifactFullScreen = false
    paneViewerOpen = false
    paneViewerURL = ''
    actionsMenuOpen = false
    modalStack = []
    artifactSearchOpen = false
    artifactQuery = ''
    artifactSearchItems = []
    focusedArtifactDismissed = false
    keyboardProxyQueue = Promise.resolve()
    keyboardProxyValue = ''
    keyboardProxyComposing = false
    keyboardProxyTextBuffer = ''
    keyboardProxyTextSession = ''
    clearTimeout(keyboardProxyFlushTimer)
    // Restore incoming session's chrome (or defaults for first visit).
    const chrome = getSessionChrome(session.name)
    artifactPaneOpen = chrome?.artifactPaneOpen ?? false
    splitMode = chrome?.splitMode ?? 'vertical'
    selectedArtifactID = chrome?.selectedArtifactID ?? null
    if (session.focused_artifact_id) {
      scheduleFocusedArtifactOpen(session.focused_artifact_id)
    }
  }

  // Bring a session to the front of the pool. If its iframe is still mounted
  // (warm hit), reuse it: re-fit, refresh, focus — no reload, no re-handshake.
  function promoteInPool(name) {
    const existing = pool.find(p => p.name === name)
    if (existing && frameHealthy[name] === false) {
      // Pooled frame holds an error page — recreate it instead of reusing.
      delete frameHealthy[name]
      pool = [{ ...existing, key: Date.now() }, ...pool.filter(p => p !== existing)]
      return
    }
    if (existing) {
      pool = [existing, ...pool.filter(p => p !== existing)]
      tick().then(async () => {
        iframeEl = frameEls[name]
        // Warm reuse skips the iframe `load` event, so ensure input listeners
        // are attached (no-op if they already are).
        attachFrameInputListeners(iframeEl)
        // Pooled switch: terminal is already connected. Record near-zero
        // switch timings (warm path) and resync size/focus.
        markIframeLoad(name)
        markTerminalReady(name)
        triggerFitAddon()
        await new Promise(r => setTimeout(r, FITADDON_SETTLE_MS))
        try { await refreshTerminal(name) } catch { /* ignore */ }
        focusTerminalSoon()
        setTimeout(restoreStoredWindow, 0)
        resizeObserver?.disconnect()
        if (iframeEl) {
          resizeObserver = new ResizeObserver(scheduleRefresh)
          resizeObserver.observe(iframeEl)
        }
      })
    } else {
      pool = [{ name, key: 0 }, ...pool].slice(0, IFRAME_POOL_MAX)
      // Drop element refs for evicted sessions so they can be GC'd.
      const live = new Set(pool.map(p => p.name))
      for (const k of Object.keys(frameEls)) {
        if (!live.has(k)) delete frameEls[k]
      }
    }
  }
  $: if (session?.focused_artifact_id && !artifactPaneOpen && !focusedArtifactDismissed) {
    scheduleFocusedArtifactOpen(session.focused_artifact_id)
  }
  $: if (artifactEvent?.nonce && artifactEvent.nonce !== lastArtifactEventNonce && artifactEvent.session === session.name) {
    lastArtifactEventNonce = artifactEvent.nonce
    focusedArtifactDismissed = false
    selectedArtifactID = artifactEvent.artifact_id
    artifactPaneOpen = true
    if (splitMode === 'terminal') splitMode = 'vertical'
  }

  function scheduleFocusedArtifactOpen(id) {
    clearTimeout(focusedArtifactTimer)
    focusedArtifactTimer = setTimeout(() => {
      if (focusedArtifactDismissed || !session?.focused_artifact_id) return
      selectedArtifactID = id
      artifactPaneOpen = true
    }, 700)
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
        reloadActiveFrame()
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
    // Fallback: at minimum route events to the iframe window. Desktop Wails
    // terminal frames are intentionally cross-origin (wails:// parent,
    // 127.0.0.1 iframe), so ask the injected terminal helper to focus xterm's
    // textarea from inside the iframe. WKWebView still won't always transfer
    // keyboard focus to a cross-origin iframe programmatically, so keep a tiny
    // parent-side keyboard proxy focused as a desktop fallback and forward keys
    // to tmux until the user manually clicks inside the terminal frame.
    iframeEl?.focus()
    try {
      const targetOrigin = new URL(iframeEl?.src || frameURL(session.name), window.location.href).origin
      iframeEl?.contentWindow?.postMessage({ type: 'devx:focus-terminal' }, targetOrigin)
    } catch { /* ignore */ }
    if (typeof window !== 'undefined' && window.__DEVX_DESKTOP) keyboardProxyEl?.focus()
  }

  function focusTerminalSoon() {
    focusTerminal()
    // WebKit often ignores the first focus while swapping iframe visibility or
    // immediately after navigation. Retry across a few frames so session
    // switches land keyboard focus in the active terminal without a click.
    setTimeout(focusTerminal, 0)
    setTimeout(focusTerminal, 60)
    setTimeout(focusTerminal, 180)
    setTimeout(focusTerminal, 360)
  }

  export function focusTerminalSurface() {
    focusTerminalSoon()
  }

  // Ctrl+Shift+S, registered on the iframe's document in capture phase so
  // xterm never sees it. Dispatches to the parent window (lexical `window`
  // is the parent since this function is defined in the parent scope).
  function toggleComposer() {
    composerOpen = !composerOpen
  }

  function closeComposer() {
    composerOpen = false
    focusTerminal()
  }

  function windowHotkey(e) {
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault()
      toggleComposer()
    }
  }

  function handleTerminalAppShortcut(e) {
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault()
      e.stopPropagation()
      toggleComposer()
      return true
    }
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'p' || e.key === 'P')) {
      e.preventDefault()
      e.stopPropagation()
      window.dispatchEvent(new CustomEvent('devx:quickSwitcher'))
      return true
    }
    if (e.ctrlKey && e.shiftKey && (e.key === 's' || e.key === 'S')) {
      e.preventDefault()
      e.stopPropagation()
      window.dispatchEvent(new CustomEvent('devx:focusSessionList'))
      return true
    }
    if (e.ctrlKey && e.shiftKey && (e.key === 'c' || e.key === 'C')) {
      e.preventDefault()
      e.stopPropagation()
      window.dispatchEvent(new CustomEvent('devx:newSession'))
      return true
    }
    if (e.ctrlKey && e.shiftKey && (e.key === 'a' || e.key === 'A')) {
      e.preventDefault()
      e.stopPropagation()
      toggleArtifacts()
      return true
    }
    if (e.ctrlKey && e.shiftKey && (e.key === 'o' || e.key === 'O')) {
      e.preventDefault()
      e.stopPropagation()
      cycleSplitMode()
      return true
    }
    if ((artifactTriggerKey === 'Ctrl+Space' && e.ctrlKey && !e.metaKey && !e.altKey && e.key === ' ') || (!e.ctrlKey && !e.metaKey && !e.altKey && artifactTriggerKey.length === 1 && e.key === artifactTriggerKey)) {
      e.preventDefault()
      e.stopPropagation()
      openArtifactSearch('insert')
      return true
    }
    return false
  }

  function iframeHotkey(e) {
    handleTerminalAppShortcut(e)
  }

  // Single owner of terminal-iframe image paste, for both browser and desktop.
  // The ttyd page injects terminalPasteBridgeScript, which reads its own paste
  // event (the parent cannot attach a listener across the cross-origin iframe in
  // the desktop app) and forwards it here via postMessage. Two message shapes:
  //   devx:terminal-image-paste     — a clipboard image File, shipped as dataURL
  //   devx:terminal-clipboard-image — no File present; try the native clipboard
  function handleTerminalMessage(e) {
    // Only trust messages from the *active* session's terminal iframe. Compare
    // against the live active frame (not iframeEl, which lags behind the active
    // session by a tick during pool promotion) so a background pooled frame
    // can't route an upload under the newly-active session's name.
    const activeFrame = frameEls[session.name]
    if (!activeFrame || e.source !== activeFrame.contentWindow) return
    const data = e.data
    if (!data || typeof data !== 'object') return
    if (data.type === 'devx:terminal-image-paste' && typeof data.dataURL === 'string') {
      const comma = data.dataURL.indexOf(',')
      const base64 = comma >= 0 ? data.dataURL.slice(comma + 1) : ''
      if (!base64) return
      processImageFile(fileFromBase64({
        name: typeof data.name === 'string' ? data.name : 'clipboard.png',
        type: typeof data.mime === 'string' ? data.mime : 'image/png',
        data: base64,
      }))
    } else if (data.type === 'devx:terminal-clipboard-image' && isDesktop()) {
      handleDesktopClipboardPaste()
    } else if (data.type === 'devx:openExternal' && typeof data.url === 'string') {
      // The ttyd page's injected window.open override forwards clicked terminal
      // URLs here so the desktop shell opens them in the user's real browser.
      if (/^https?:\/\//.test(data.url)) openExternal(data.url)
    }
  }

  function tmuxKeyForEvent(e) {
    const named = {
      Enter: 'Enter', Tab: 'Tab', Escape: 'Escape', Backspace: 'BSpace', Delete: 'Delete',
      ArrowUp: 'Up', ArrowDown: 'Down', ArrowLeft: 'Left', ArrowRight: 'Right',
      Home: 'Home', End: 'End', PageUp: 'PageUp', PageDown: 'PageDown', Insert: 'IC',
    }
    if (named[e.key]) return named[e.key]
    if (e.ctrlKey && !e.metaKey && !e.altKey && e.key?.length === 1 && /[a-zA-Z]/.test(e.key)) {
      return 'C-' + e.key.toLowerCase()
    }
    if (e.altKey && !e.metaKey && !e.ctrlKey && e.key?.length === 1) {
      return 'M-' + e.key
    }
    return ''
  }

  function enqueueKeyboardProxyInput(sessionName, send) {
    keyboardProxyQueue = keyboardProxyQueue.catch(() => {}).then(() => send(sessionName))
    return keyboardProxyQueue
  }

  function flushKeyboardProxyText() {
    clearTimeout(keyboardProxyFlushTimer)
    const text = keyboardProxyTextBuffer
    const sessionName = keyboardProxyTextSession
    keyboardProxyTextBuffer = ''
    keyboardProxyTextSession = ''
    if (!text || !sessionName) return keyboardProxyQueue
    return enqueueKeyboardProxyInput(sessionName, (name) => sendInput(name, text, { mode: 'literal' }))
  }

  function bufferKeyboardProxyText(sessionName, text) {
    if (!text) return
    if (keyboardProxyTextSession && keyboardProxyTextSession !== sessionName) {
      flushKeyboardProxyText()
    }
    keyboardProxyTextSession = sessionName
    keyboardProxyTextBuffer += text
    clearTimeout(keyboardProxyFlushTimer)
    keyboardProxyFlushTimer = setTimeout(flushKeyboardProxyText, 75)
  }

  function handleKeyboardProxyKeydown(e) {
    if (composerOpen || artifactSearchOpen || paneViewerOpen || artifactFullScreen) return
    if (handleTerminalAppShortcut(e)) return
    if (e.metaKey) return
    const key = tmuxKeyForEvent(e)
    if (!key) return
    e.preventDefault()
    e.stopPropagation()
    const sessionName = session.name
    flushKeyboardProxyText()
    enqueueKeyboardProxyInput(sessionName, (name) => sendKey(key, name))
  }

  function handleKeyboardProxyInput(e) {
    if (keyboardProxyComposing || e?.isComposing) return
    const text = keyboardProxyValue
    const sessionName = session.name
    keyboardProxyValue = ''
    if (!text || composerOpen || artifactSearchOpen || paneViewerOpen || artifactFullScreen) return
    bufferKeyboardProxyText(sessionName, text)
  }

  function handleKeyboardProxyCompositionStart() {
    keyboardProxyComposing = true
  }

  function handleKeyboardProxyCompositionEnd() {
    keyboardProxyComposing = false
    handleKeyboardProxyInput()
  }

  function handleKeyboardProxyPaste(e) {
    if (composerOpen || artifactSearchOpen || paneViewerOpen || artifactFullScreen) return
    const items = e.clipboardData?.items || []
    for (const item of items) {
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        e.preventDefault()
        processImageFile(item.getAsFile())
        return
      }
    }
    const text = e.clipboardData?.getData('text/plain') || ''
    if (text) {
      e.preventDefault()
      const sessionName = session.name
      flushKeyboardProxyText()
      enqueueKeyboardProxyInput(sessionName, (name) => sendInput(name, text))
    }
  }

  // Timing constants for xterm.js / FitAddon initialisation.
  const XTERM_POLL_DEADLINE_MS = 5000  // max time to wait for xterm.js init
  const XTERM_POLL_INTERVAL_MS = 100   // polling interval while waiting
  const FITADDON_SETTLE_MS     = 200   // time for FitAddon → ioctl to propagate

  function pushModalHistory(type) {
    modalStack = [...modalStack, type]
    try { history.pushState({ devxModal: type }, '', window.location.href) } catch { /* ignore */ }
  }

  function openArtifactFullScreen() {
    if (!artifactFullScreen) pushModalHistory('artifact-fullscreen')
    artifactFullScreen = true
  }

  function popModalHistory(type) {
    const top = modalStack[modalStack.length - 1]
    modalStack = modalStack.filter((_, i) => i !== modalStack.length - 1)
    if (top === type && history.state?.devxModal === type) {
      try {
        suppressNextPopState = true
        history.back()
      } catch {
        suppressNextPopState = false
      }
    }
  }

  function closeArtifactFullScreen(goBack = true) {
    artifactFullScreen = false
    if (goBack) popModalHistory('artifact-fullscreen')
  }

  function closePaneViewer(goBack = true) {
    paneViewerOpen = false
    paneViewerURL = ''
    if (goBack) popModalHistory('pane-viewer')
  }

  function openPaneViewerFromMenu() {
    actionsMenuOpen = false
    openPaneViewer()
  }

  function openPasteArtifactFromMenu() {
    actionsMenuOpen = false
    openPasteArtifact()
  }

  function openArtifactSearchFromMenu() {
    actionsMenuOpen = false
    openArtifactSearch('insert')
  }

  function toggleArtifactsFromMenu() {
    actionsMenuOpen = false
    toggleArtifacts()
  }

  function cycleSplitModeFromMenu() {
    actionsMenuOpen = false
    cycleSplitMode()
  }

  function openImagePickerFromMenu() {
    actionsMenuOpen = false
    fileInputEl?.click()
  }

  function handleDesktopCommand(e) {
    switch (e.detail || e.type.replace('devx:terminal:', '')) {
      case 'composer': toggleComposer(); break
      case 'artifacts': toggleArtifacts(); break
      case 'split': cycleSplitMode(); break
      case 'view-output': openPaneViewer(); break
      case 'insert-artifact': openArtifactSearch('insert'); break
      case 'new-artifact': openPasteArtifact(); break
      case 'focus': focusTerminalSoon(); break
    }
  }

  async function openPaneViewer() {
    const params = new URLSearchParams({ name: session.name })
    try {
      const pane = await getActivePane(session.name)
      if (pane != null && !Number.isNaN(Number(pane))) params.set('pane', String(pane))
    } catch {
      // Fall back to tmux's current active pane if the lookup fails.
    }
    paneViewerURL = `/api/pane-content/view?${params.toString()}`
    paneViewerOpen = true
    pushModalHistory('pane-viewer')
  }

  function handlePopState() {
    if (suppressNextPopState) {
      suppressNextPopState = false
      return
    }
    const type = modalStack[modalStack.length - 1]
    modalStack = modalStack.filter((_, i) => i !== modalStack.length - 1)
    if (type === 'pane-viewer') {
      closePaneViewer(false)
      return
    }
    if (type === 'artifact-fullscreen') {
      closeArtifactFullScreen(false)
      return
    }
    if (type === 'artifact-search') {
      closeArtifactSearch(false)
      return
    }
    if (paneViewerOpen) {
      closePaneViewer(false)
      return
    }
    if (artifactFullScreen) {
      closeArtifactFullScreen(false)
      return
    }
    if (artifactSearchOpen) {
      closeArtifactSearch(false)
      return
    }
    if (actionsMenuOpen) {
      actionsMenuOpen = false
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
  // Per-frame input listeners (paste/keydown/drag/drop) must be attached to
  // each pooled iframe's contentDocument exactly once. These are separate from
  // the active-session resync work because a pooled iframe can finish loading
  // while a *different* session is active, and warm pool promotions reuse an
  // already-loaded iframe without firing another `load` event. If listener
  // registration lived only in handleIframeLoad (gated on the active session),
  // such frames would silently lose paste/drag support. Dedup + attach logic
  // lives in ./terminal/frameInputListeners.js (unit tested).
  function attachFrameInputListeners(frameEl) {
    attachListeners(frameEl?.contentDocument, {
      onKeydown: iframeHotkey,
      // Image paste inside the iframe is owned by terminalPasteBridgeScript
      // (injected into the ttyd page), which forwards via postMessage to
      // handleTerminalMessage and works across the cross-origin boundary. So no
      // onPaste handler is registered here, to avoid a duplicate paste pipeline.
      // Drag events do not bubble across iframe boundaries, so a file dragged
      // over the iframe never reaches the outer div's dragenter/drop handlers.
      // Mirror the events onto the parent window so the drop overlay appears
      // and the file is processed correctly.
      onDragEnter: (e) => {
        const hasFiles = Array.from(e.dataTransfer?.items || []).some(i => i.kind === 'file')
        if (hasFiles) { dragCounter++; isDragOver = true }
      },
      onDragLeave: () => {
        dragCounter--
        if (dragCounter <= 0) { dragCounter = 0; isDragOver = false }
      },
      onDragOver: (e) => e.preventDefault(),
      onDrop: (e) => {
        e.preventDefault()
        dragCounter = 0; isDragOver = false
        const files = Array.from(e.dataTransfer?.files || [])
        if (files.length) processImageFiles(files)
      },
    })
  }

  async function handleIframeLoad() {
    markIframeLoad(session.name)
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
    let xtermReady = false
    while (Date.now() < deadline) {
      try {
        if (iframeEl?.contentDocument?.querySelector('.xterm-helper-textarea')) { xtermReady = true; break }
      } catch { /* cross-origin / not-yet-loaded */ }
      await new Promise(r => setTimeout(r, XTERM_POLL_INTERVAL_MS))
    }
    // Record health so the keep-alive pool never serves a cached error page.
    frameHealthy[session.name] = xtermReady
    // Re-trigger FitAddon so it sends the current browser viewport dimensions
    // to the PTY. Small wait after so ioctl has time to propagate before the
    // subsequent refresh-client call.
    markTerminalReady(session.name)
    triggerFitAddon()
    await new Promise(r => setTimeout(r, FITADDON_SETTLE_MS))
    try { await refreshTerminal(session.name) } catch { /* ignore */ }
    focusTerminalSoon()
    // Restore window tabs after the terminal is interactive; don't block the first
    // usable paint/focus on tmux bookkeeping.
    setTimeout(restoreStoredWindow, 0)
    // Register input listeners after focus so xterm is initialised. Idempotent
    // and shared with the warm pool-promotion path.
    attachFrameInputListeners(iframeEl)
    // Watch for iframe size changes (mobile browser chrome, keyboard, orientation)
    resizeObserver?.disconnect()
    resizeObserver = new ResizeObserver(scheduleRefresh)
    resizeObserver.observe(iframeEl)
  }

  async function sendKey(key, sessionName = session.name) {
    try { await apiSendKeys(sessionName, key) } catch { /* ignore */ }
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
      setToastUpload(null)
      toastError = `Unsupported type: ${files[0].type || files[0].name || 'unknown'}`
      return
    }

    uploading = true
    const objectURLs = valid.map(f => URL.createObjectURL(f))

    try {
      const results = await Promise.all(valid.map(uploadOneImage))
      const paths = results.map(r => r.path)
      const joined = paths.join(' ') + ' '
      // When the composer overlay is open, insert the path(s) into the composer
      // textarea instead of the tmux pane so a paste/drop while composing lands
      // where the user is typing. Otherwise inject into the active tmux pane
      // (no Enter — user confirms). sendLiteral preserves spaces in paths.
      // composerComponent can briefly lag composerOpen during mount, so wait a
      // tick for the bind before deciding where the paths go.
      if (composerOpen && !composerComponent) await tick()
      if (composerOpen && composerComponent) {
        composerComponent.insertText(joined)
      } else {
        await sendLiteral(session.name, joined)
      }
      toastError = null
      setToastUpload({
        path: paths.length === 1 ? paths[0] : `${paths.length} images uploaded`,
        objectURL: objectURLs[0],
      })
      // Revoke extra objectURLs not used by the toast preview.
      objectURLs.slice(1).forEach(u => URL.revokeObjectURL(u))
    } catch (e) {
      objectURLs.forEach(u => URL.revokeObjectURL(u))
      setToastUpload(null)
      toastError = e.message || 'Upload failed'
    } finally {
      uploading = false
    }
  }

  // Replace (or clear) the upload toast, revoking the previous preview object
  // URL first so a superseded preview doesn't leak. dismissToast/onDestroy
  // revoke directly when tearing down; every other mutation goes through here.
  function setToastUpload(next) {
    if (toastUpload?.objectURL && toastUpload.objectURL !== next?.objectURL) {
      URL.revokeObjectURL(toastUpload.objectURL)
    }
    toastUpload = next
  }

  // Upload a single image. In the desktop shell, route through the native host
  // binding (uploadOneImageDesktop) because WKWebView strips the body of POST
  // requests issued from the WebView, so a multipart fetch through the Wails
  // proxy arrives empty. In a browser, use the normal fetch upload.
  async function uploadOneImage(file) {
    if (isDesktop()) return uploadOneImageDesktop(file)
    return uploadImage(file, session.name)
  }

  async function uploadOneImageDesktop(file) {
    const data = await fileToBase64(file)
    const res = await desktopUploadImage({ name: file.name, session: session.name, data })
    if (!res) return uploadImage(file, session.name) // no host binding; fall back
    return res
  }

  function fileToBase64(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onerror = () => reject(reader.error || new Error('read failed'))
      reader.onload = () => {
        // reader.result is a data URL: strip the "data:<mime>;base64," prefix.
        const comma = String(reader.result).indexOf(',')
        resolve(String(reader.result).slice(comma + 1))
      }
      reader.readAsDataURL(file)
    })
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

  async function closeArtifacts() {
    const wasFullScreen = artifactFullScreen
    focusedArtifactDismissed = true
    artifactPaneOpen = false
    artifactFullScreen = false
    selectedArtifactID = null
    if (wasFullScreen) popModalHistory('artifact-fullscreen')
    try { await clearArtifactFocus(session.name) } catch { /* ignore */ }
    await tick()
    focusTerminal()
  }

  function openViewerPane() {
    focusedArtifactDismissed = false
    artifactPaneOpen = true
    if (splitMode === 'terminal') splitMode = 'vertical'
  }

  function openPasteArtifact() {
    openViewerPane()
    pasteArtifactNonce++
  }

  function toggleArtifacts() {
    if (artifactPaneOpen && splitMode !== 'terminal') {
      closeArtifacts()
      return
    }
    openViewerPane()
  }

  function cycleSplitMode() {
    focusedArtifactDismissed = false
    artifactPaneOpen = true
    const modes = ['vertical', 'horizontal', 'artifacts', 'terminal']
    const idx = modes.indexOf(splitMode)
    splitMode = modes[(idx + 1) % modes.length]
  }

  async function openArtifactSearch(mode = 'insert') {
    if (!artifactSearchOpen) pushModalHistory('artifact-search')
    artifactOverlayMode = mode
    try { artifactSearchItems = await listArtifacts(session.name) } catch { artifactSearchItems = [] }
    artifactQuery = ''
    artifactSearchIndex = 0
    artifactSearchOpen = true
  }

  function openArtifactInPane(artifact) {
    if (!artifact) return
    focusedArtifactDismissed = false
    selectedArtifactID = artifact.id
    artifactPaneOpen = true
    if (splitMode === 'terminal') splitMode = 'vertical'
    closeArtifactSearch()
  }

  $: if (artifactSearchIndex >= filteredArtifactSearchItems.length) artifactSearchIndex = Math.max(0, filteredArtifactSearchItems.length - 1)

  function moveArtifactSearch(delta) {
    artifactSearchIndex = Math.max(0, Math.min(filteredArtifactSearchItems.length - 1, artifactSearchIndex + delta))
  }

  async function chooseArtifactFromOverlay(artifact) {
    if (!artifact) return
    if (artifactOverlayMode === 'open') {
      openArtifactInPane(artifact)
      return
    }
    await insertArtifactPath(artifact.path)
  }

  async function insertArtifactPath(path) {
    closeArtifactSearch()
    await sendLiteral(session.name, path + ' ')
    focusTerminal()
  }

  function closeArtifactSearch(goBack = true) {
    artifactSearchOpen = false
    focusTerminal()
    if (goBack) popModalHistory('artifact-search')
  }

  function handleComposerSent(event) {
    scheduleRefresh()
    // Sending from the desktop overlay dismisses it; paste-only keeps it open
    // so the user can continue composing while reviewing the staged text.
    if (composerOpen && event.detail?.submit) {
      composerOpen = false
    }
    if (!composerOpen) focusTerminal()
  }

  function handleComposerImagePaste(event) {
    processImageFiles(event.detail?.files || [])
  }

  // Exported so App.svelte can route parent-window paste events here.
  export function handleImagePaste(file) {
    processImageFile(file)
  }

  // Exported so the desktop shell bridge can route native file drops here.
  export function handleImageFiles(files) {
    processImageFiles(files)
  }

  // Exported so the desktop file-drop bridge can surface host-side rejections
  // (oversize/unreadable/unsupported) through the same toast the web flow uses.
  export function showUploadError(message) {
    setToastUpload(null)
    toastError = message
  }

  // Single owner of desktop clipboard-image paste. Both the iframe paste handler
  // (xterm focused) and App.svelte's window paste handler (parent focused) call
  // this; the guard collapses any double-dispatch for one Cmd/Ctrl+V into a
  // single native ClipboardImage IPC + upload. Exported for App.svelte.
  let desktopClipboardPasteInFlight = false
  export async function handleDesktopClipboardPaste() {
    if (desktopClipboardPasteInFlight) return
    desktopClipboardPasteInFlight = true
    try {
      const data = await clipboardImage()
      if (!data) return
      processImageFile(fileFromBase64({ name: 'clipboard.png', type: 'image/png', data }))
    } catch { /* no clipboard image available */ } finally {
      desktopClipboardPasteInFlight = false
    }
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

  function handleDocumentClick(e) {
    if (!actionsMenuOpen) return
    if (!e.target?.closest?.('[data-actions-menu]')) actionsMenuOpen = false
  }

  onMount(() => {
    // Defer non-critical chrome/artifact settings so switching sessions prioritizes
    // the terminal iframe connection first.
    setTimeout(loadWindows, 250)
    setTimeout(() => getSettings().then(settings => { artifactTriggerKey = settings.artifact_trigger_key || 'Ctrl+Space' }).catch(() => {}), 500)
    windowPollTimer = setInterval(loadWindows, 3000)
    // visualViewport fires on mobile when the address bar hides/shows or the
    // soft keyboard appears — more reliable than ResizeObserver alone.
    window.visualViewport?.addEventListener('resize', scheduleRefresh)
    document.addEventListener('visibilitychange', handleVisibilityChange)
    window.addEventListener('popstate', handlePopState)
    document.addEventListener('click', handleDocumentClick)
    window.addEventListener('keydown', windowHotkey)
    window.addEventListener('devx:terminal:composer', handleDesktopCommand)
    window.addEventListener('devx:terminal:artifacts', handleDesktopCommand)
    window.addEventListener('devx:terminal:split', handleDesktopCommand)
    window.addEventListener('devx:terminal:view-output', handleDesktopCommand)
    window.addEventListener('devx:terminal:insert-artifact', handleDesktopCommand)
    window.addEventListener('devx:terminal:new-artifact', handleDesktopCommand)
    window.addEventListener('devx:terminal:focus', handleDesktopCommand)
    window.addEventListener('message', handleTerminalMessage)
  })
  onDestroy(() => {
    clearInterval(windowPollTimer)
    clearTimeout(resizeTimer)
    clearTimeout(focusedArtifactTimer)
    clearTimeout(keyboardProxyFlushTimer)
    resizeObserver?.disconnect()
    window.visualViewport?.removeEventListener('resize', scheduleRefresh)
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    window.removeEventListener('popstate', handlePopState)
    document.removeEventListener('click', handleDocumentClick)
    window.removeEventListener('keydown', windowHotkey)
    window.removeEventListener('devx:terminal:composer', handleDesktopCommand)
    window.removeEventListener('devx:terminal:artifacts', handleDesktopCommand)
    window.removeEventListener('devx:terminal:split', handleDesktopCommand)
    window.removeEventListener('devx:terminal:view-output', handleDesktopCommand)
    window.removeEventListener('devx:terminal:insert-artifact', handleDesktopCommand)
    window.removeEventListener('devx:terminal:new-artifact', handleDesktopCommand)
    window.removeEventListener('devx:terminal:focus', handleDesktopCommand)
    window.removeEventListener('message', handleTerminalMessage)
    if (toastUpload?.objectURL) URL.revokeObjectURL(toastUpload.objectURL)
  })
</script>

<!-- Fill parent container (flex-1 set by App.svelte) -->
<!--
  --wails-drop-target:drop marks this subtree as a valid native drop target in
  the desktop shell. Wails' OnFileDrop callback rejects the drop unless
  document.elementFromPoint(dropX, dropY) carries this CSS property, and on
  macOS the drop lands on the terminal <iframe>, so the property is set here and
  on the iframe below. It is inert in the browser PWA (no Wails runtime).
-->
<div
  class="flex flex-col flex-1 min-h-0 bg-black relative"
  style="--wails-drop-target: drop;"
  role="region"
  aria-label="terminal with image drop target"
  on:dragenter={handleDragEnter}
  on:dragleave={handleDragLeave}
  on:dragover={handleDragOver}
  on:drop={handleDrop}
>

  <textarea
    bind:this={keyboardProxyEl}
    bind:value={keyboardProxyValue}
    aria-hidden="true"
    autocomplete="off"
    autocapitalize="off"
    spellcheck="false"
    class="fixed w-px h-px opacity-0 pointer-events-none -left-10 top-0"
    on:keydown={handleKeyboardProxyKeydown}
    on:input={handleKeyboardProxyInput}
    on:compositionstart={handleKeyboardProxyCompositionStart}
    on:compositionend={handleKeyboardProxyCompositionEnd}
    on:paste={handleKeyboardProxyPaste}
  ></textarea>

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

    <div class="hidden lg:flex items-stretch shrink-0">
      <button
        on:click={openPaneViewer}
        title="open current terminal output in fullscreen"
        class="px-3 text-gray-500 hover:text-cyan-300 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center justify-center transition-colors min-w-[58px]"
      >[view]</button>

      <!-- Artifact actions -->
      <button
        on:click={openPasteArtifact}
        title="create/paste a text artifact"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >[new artifact]</button>

      <button
        on:click={() => openArtifactSearch('insert')}
        title="insert an artifact path reference into the terminal"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >[insert ref]</button>
      <button
        on:click={toggleArtifacts}
        title="open/close artifacts panel (Ctrl+Shift+A)"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >{artifactsIsVisible ? '[hide artifacts]' : '[artifacts]'}</button>
      <button
        on:click={cycleSplitMode}
        title="cycle artifact split layout (Ctrl+Shift+O)"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >[split: {splitMode}]</button>

      <!-- Compose overlay (Cmd/Ctrl+K) -->
      <button
        on:click={toggleComposer}
        title="compose a prompt outside the terminal (⌘/Ctrl+K)"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >[compose]</button>

      <!-- Attach image button -->
      <button
        on:click={() => fileInputEl?.click()}
        title="attach image"
        class="px-3 text-gray-600 hover:text-cyan-400 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center transition-colors"
      >[img]</button>
    </div>

    <MobileActionsMenu
      open={actionsMenuOpen}
      {artifactsIsVisible}
      {splitMode}
      onToggle={() => actionsMenuOpen = !actionsMenuOpen}
      onView={openPaneViewerFromMenu}
      onAttachImage={openImagePickerFromMenu}
      onNewArtifact={openPasteArtifactFromMenu}
      onInsertArtifact={openArtifactSearchFromMenu}
      onToggleArtifacts={toggleArtifactsFromMenu}
      onCycleSplit={cycleSplitModeFromMenu}
    />
    <input
      bind:this={fileInputEl}
      type="file"
      accept="image/png,image/jpeg,image/gif,image/webp"
      multiple
      class="hidden"
      on:change={handleFileInput}
    />
  </div>

  <div class="flex-1 min-h-0 flex {splitMode === 'horizontal' ? 'flex-col' : 'flex-row'}">
    {#if terminalIsVisible}
      <div class="min-h-0 min-w-0 flex flex-col relative" style={terminalPaneCSS}>
        <!--
          Keep-alive pool: every pooled session's iframe stays mounted in this
          container; only the active one is visible. Hidden frames keep their
          WebSocket + xterm state alive, making switch-back instant. Keyed per
          (name, key) so evicting/reloading destroys the element rather than
          navigating it (navigation would trigger ttyd's beforeunload dialog).
          Hidden frames use visibility:hidden so they keep real layout geometry
          for xterm, plus pointer-events:none and tabindex=-1 so they can't
          steal clicks or keyboard focus.
        -->
        {#each pool as frame (frame.name + '::' + frame.key)}
          {@const isActiveFrame = frame.name === session.name}
          <iframe
            bind:this={frameEls[frame.name]}
            src={frameURL(frame.name)}
            title="Terminal — {frame.name}"
            class="absolute inset-0 w-full h-full border-0"
            style="visibility: {isActiveFrame ? 'visible' : 'hidden'}; pointer-events: {isActiveFrame ? 'auto' : 'none'}; z-index: {isActiveFrame ? 1 : 0}; --wails-drop-target: drop;"
            tabindex={isActiveFrame ? 0 : -1}
            allow="clipboard-read; clipboard-write"
            on:load={() => {
              // Always attach per-frame input listeners, even for a frame that
              // finished loading while another session is active — otherwise it
              // would have no paste/drag support when later promoted warm.
              attachFrameInputListeners(frameEls[frame.name])
              if (frame.name === session.name) { iframeEl = frameEls[frame.name]; handleIframeLoad() }
            }}
          ></iframe>
        {/each}
      </div>
    {/if}

    {#if artifactsIsVisible && !artifactFullScreen}
      <div class="min-h-0 min-w-0" style={artifactPaneCSS}>
        {#key session.name}
          <ArtifactPane {session} selectedArtifactID={selectedArtifactID} {pasteArtifactNonce} fullScreen={false} onToggleFullScreen={openArtifactFullScreen} onInsert={insertArtifactPath} onClose={() => { closeArtifacts(); splitMode = 'vertical' }} />
        {/key}
      </div>
    {/if}
  </div>

  {#if artifactsIsVisible && artifactFullScreen}
    <div class="fixed inset-0 z-[1000] bg-[#0b1020] border border-[#1e2d4a] shadow-2xl">
      {#key session.name}
        <ArtifactPane {session} selectedArtifactID={selectedArtifactID} {pasteArtifactNonce} fullScreen={true} onToggleFullScreen={() => closeArtifactFullScreen()} onInsert={insertArtifactPath} onClose={() => { closeArtifacts(); splitMode = 'vertical' }} />
      {/key}
    </div>
  {/if}

  {#if paneViewerOpen}
    <PaneViewerModal {session} url={paneViewerURL} onClose={() => closePaneViewer()} />
  {/if}

  <!-- Desktop: transient composer overlay (Cmd/Ctrl+K) -->
  {#if composerOpen}
    <PromptComposer bind:this={composerComponent} variant="overlay" sessionName={session.name} on:sent={handleComposerSent} on:close={closeComposer} on:imagepaste={handleComposerImagePaste} on:desktopclipboardimage={handleDesktopClipboardPaste} />
  {/if}

  <!-- Mobile: docked composer is THE input; terminal is mostly a display surface.
       The soft keybar is collapsed behind the ⌨ toggle to save vertical space. -->
  {#if terminalIsVisible}
    <div class="lg:hidden">
      <PromptComposer
        variant="docked"
        sessionName={session.name}
        keysOpen={softKeysOpen}
        on:sent={handleComposerSent}
        on:layoutchange={scheduleRefresh}
        on:imagepaste={handleComposerImagePaste}
        on:desktopclipboardimage={handleDesktopClipboardPaste}
        on:togglekeys={() => { softKeysOpen = !softKeysOpen; scheduleRefresh() }}
      />
      {#if softKeysOpen}
        <SoftKeybar onKey={sendKey} />
      {/if}
    </div>
  {/if}

  {#if artifactSearchOpen}
    <ArtifactSearchOverlay
      mode={artifactOverlayMode}
      query={artifactQuery}
      items={filteredArtifactSearchItems}
      selectedIndex={artifactSearchIndex}
      onQuery={(value) => { artifactQuery = value; artifactSearchIndex = 0 }}
      onClose={() => closeArtifactSearch()}
      onMove={moveArtifactSearch}
      onChoose={chooseArtifactFromOverlay}
      onOpen={openArtifactInPane}
      onInsert={insertArtifactPath}
    />
  {/if}

  <!-- Image upload confirmation / error toast -->
  {#if toastUpload || toastError}
    <ImageToast upload={toastUpload} error={toastError} onDismiss={dismissToast} />
  {/if}

</div>
