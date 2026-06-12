<!-- web/app/src/App.svelte -->
<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { isLoggedIn, subscribeToEvents, unflagSession } from './api.js'
  import { requestNotificationPermission, notifyFlag } from './lib/notifications.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  import Terminal from './lib/Terminal.svelte'
  import QuickSwitcher from './lib/QuickSwitcher.svelte'
  import { markSwitchStart } from './lib/stores/sessionUiState.js'
  import ImageToast from './lib/ImageToast.svelte'
  import FlagToast from './lib/FlagToast.svelte'
  import ShareTarget from './lib/ShareTarget.svelte'

  // view is only used on mobile to toggle between sessions and terminal.
  // On desktop, both panels are always visible.
  let view = 'sessions'  // 'sessions' | 'terminal'
  let activeSession = null
  let terminalComponent

  // Reactive: determined at page load from the localStorage marker set on login.
  // The marker is non-sensitive — the actual auth token lives in an httpOnly
  // cookie. Any API 401 clears the marker and reloads to show the login screen.
  let loggedIn = isLoggedIn()

  // Remote show toast: set when a "show" SSE event arrives from the CLI.
  let remoteShow = null  // { url, name } | null

  // Flag toast: set when a "flag" SSE event arrives and native notifications
  // are unavailable. Cleared on dismiss or after 8s.
  let flagToastEvent = null  // { session, reason } | null

  // Bumped on any flag SSE event to trigger an immediate sidebar reload.
  let sessionRefreshTrigger = 0
  // Set to the session name on flag events to trigger a flash animation.
  let flashSession = null
  let artifactEvent = null
  let shareToken = new URLSearchParams(window.location.search).get('share') || ''

  let unsubscribeSSE
  let switcherOpen = false

  function handleGlobalKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'p' || e.key === 'P')) {
      e.preventDefault()
      switcherOpen = !switcherOpen
    }
  }

  async function handleSwitcherSelect(session) {
    switcherOpen = false
    markSwitchStart(session.name)
    openTerminal(session)
    await tick()
    setTimeout(() => terminalComponent?.focusTerminalSurface?.(), 0)
    setTimeout(() => terminalComponent?.focusTerminalSurface?.(), 250)
  }

  // Named so it can be removed on destroy; dispatched by Terminal's
  // iframeHotkey bridge when Cmd/Ctrl+P is pressed inside the terminal iframe.
  function toggleSwitcher() {
    switcherOpen = !switcherOpen
  }

  function dispatchTerminalCommand(name) {
    if (name === 'focus') {
      terminalComponent?.focusTerminalSurface?.()
      return
    }
    window.dispatchEvent(new CustomEvent(`devx:terminal:${name}`))
  }
  const focusTerminalHandler = () => dispatchTerminalCommand('focus')
  const toggleComposerHandler = () => dispatchTerminalCommand('composer')
  const toggleArtifactsHandler = () => dispatchTerminalCommand('artifacts')
  const cycleSplitHandler = () => dispatchTerminalCommand('split')
  const viewTerminalOutputHandler = () => dispatchTerminalCommand('view-output')
  const insertArtifactHandler = () => dispatchTerminalCommand('insert-artifact')
  const newArtifactHandler = () => dispatchTerminalCommand('new-artifact')

  onMount(() => {
    if (loggedIn) {
      requestNotificationPermission()
      window.addEventListener('devx:quickSwitcher', toggleSwitcher)
      window.addEventListener('devx:focusTerminal', focusTerminalHandler)
      window.addEventListener('devx:toggleComposer', toggleComposerHandler)
      window.addEventListener('devx:toggleArtifacts', toggleArtifactsHandler)
      window.addEventListener('devx:cycleSplit', cycleSplitHandler)
      window.addEventListener('devx:viewTerminalOutput', viewTerminalOutputHandler)
      window.addEventListener('devx:insertArtifact', insertArtifactHandler)
      window.addEventListener('devx:newArtifact', newArtifactHandler)
      unsubscribeSSE = subscribeToEvents({
        show: (event) => {
          remoteShow = event
        },
        flag: (event) => {
          sessionRefreshTrigger++
          if (event.flagged) {
            flashSession = event.session
            setTimeout(() => { flashSession = null }, 3000)
            notifyFlag(event, {
              onNavigate: (name) => openTerminalByName(name),
              showFallback: (ev) => { flagToastEvent = ev },
            })
          }
        },
        artifact: (event) => {
          sessionRefreshTrigger++
          flashSession = event.session
          setTimeout(() => { flashSession = null }, 3000)
          if (activeSession?.name === event.session) {
            artifactEvent = { ...event, nonce: Date.now() }
          }
        },
      })
    }
  })

  onDestroy(() => {
    unsubscribeSSE?.()
    window.removeEventListener('devx:quickSwitcher', toggleSwitcher)
    window.removeEventListener('devx:focusTerminal', focusTerminalHandler)
    window.removeEventListener('devx:toggleComposer', toggleComposerHandler)
    window.removeEventListener('devx:toggleArtifacts', toggleArtifactsHandler)
    window.removeEventListener('devx:cycleSplit', cycleSplitHandler)
    window.removeEventListener('devx:viewTerminalOutput', viewTerminalOutputHandler)
    window.removeEventListener('devx:insertArtifact', insertArtifactHandler)
    window.removeEventListener('devx:newArtifact', newArtifactHandler)
  })

  function dismissRemoteShow() {
    remoteShow = null
  }

  function openTerminal(session) {
    activeSession = session
    view = 'terminal'
    if (session.attention_flag) {
      unflagSession(session.name).catch(() => {})
    }
  }

  // Opens a terminal by session name — used by notification click handlers
  // that only have the name, not the full session object. Falls back gracefully
  // if the session is not in the current list (the poll will pick it up soon).
  function openTerminalByName(name) {
    // If already active, just switch to terminal view.
    if (activeSession?.name === name) {
      view = 'terminal'
    } else {
      // We don't have the full session object here, so use a minimal stub.
      // SessionList will have the full data after its next load.
      activeSession = { name }
      view = 'terminal'
    }
    // Always clear the flag — we don't have the full object to check attention_flag.
    unflagSession(name).catch(() => {})
  }

  function goHome() {
    view = 'sessions'
    activeSession = null
  }

  function clearShareToken() {
    shareToken = ''
    const url = new URL(window.location.href)
    url.searchParams.delete('share')
    history.replaceState(history.state, '', url.pathname + url.search + url.hash)
  }

  function handleShareCreated(event) {
    clearShareToken()
    sessionRefreshTrigger++
    openTerminalByName(event.session)
    if (event.artifacts?.[0]) {
      artifactEvent = { session: event.session, artifact_id: event.artifacts[0].id, nonce: Date.now() }
    }
  }

  // Global paste handler: routes image pastes to the terminal component when
  // focus is in the parent window (e.g. sidebar). The iframe-document paste
  // handler in Terminal.svelte covers paste events when xterm has focus.
  function handleGlobalPaste(e) {
    if (!activeSession || !terminalComponent) return
    for (const item of (e.clipboardData?.items || [])) {
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (!file) return
        e.preventDefault()
        terminalComponent.handleImagePaste(file)
        return
      }
    }
  }
</script>

<svelte:window on:paste={handleGlobalPaste} on:keydown={handleGlobalKeydown} />

{#if !loggedIn}
  <Login />
{:else}
  <!--
    Two-column layout:
    - Mobile: show sidebar OR terminal (toggled via `view`)
    - Desktop (lg+): both panels always visible side by side
  -->
  <div class="relative flex h-dvh overflow-hidden bg-[#0a0e1a]">

    <!-- Session list sidebar -->
    <div class="
      flex flex-col shrink-0
      {view === 'terminal' ? 'hidden lg:flex lg:w-72 xl:w-80' : 'flex w-full lg:w-72 xl:w-80'}
      border-r border-[#1e2d4a]
    ">
      <SessionList onOpenTerminal={openTerminal} activeSessionName={activeSession?.name} onDeleteSession={goHome} refreshTrigger={sessionRefreshTrigger} {flashSession} />
    </div>

    <!-- Terminal / empty state -->
    <div class="flex-1 flex flex-col min-w-0 {view === 'sessions' ? 'hidden lg:flex' : 'flex'}">
      {#if activeSession}
        <Terminal bind:this={terminalComponent} session={activeSession} {artifactEvent} onBack={goHome} />
      {:else}
        <!-- Desktop: no session selected yet -->
        <div class="flex-1 flex flex-col items-center justify-center text-gray-700 select-none">
          <div class="text-5xl mb-4 opacity-10">⌨</div>
          <p class="text-xs font-mono tracking-widest uppercase opacity-50">select a session</p>
        </div>
      {/if}
    </div>

    <!-- Remote show toast: triggered by devx show <path> from the CLI -->
    {#if remoteShow}
      <ImageToast
        upload={{ path: remoteShow.name, objectURL: remoteShow.url, url: remoteShow.url }}
        onDismiss={dismissRemoteShow}
        sticky={true}
      />
    {/if}

    <!-- Flag toast: fallback when native notification permission is denied -->
    <FlagToast
      flagEvent={flagToastEvent}
      onDismiss={() => { flagToastEvent = null }}
      onNavigate={openTerminalByName}
    />

    {#if shareToken}
      <ShareTarget token={shareToken} onCancel={clearShareToken} onCreated={handleShareCreated} />
    {/if}

    <!-- Quick switcher: Cmd/Ctrl+P fuzzy session jump -->
    {#if switcherOpen}
      <QuickSwitcher onSelect={handleSwitcherSelect} onClose={() => switcherOpen = false} />
    {/if}

  </div>
{/if}
