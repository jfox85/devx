<!-- web/app/src/App.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { isLoggedIn, subscribeToEvents, unflagSession } from './api.js'
  import { requestNotificationPermission, notifyFlag } from './lib/notifications.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  import Terminal from './lib/Terminal.svelte'
  import ImageToast from './lib/ImageToast.svelte'
  import FlagToast from './lib/FlagToast.svelte'

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

  let unsubscribeSSE

  onMount(() => {
    if (loggedIn) {
      requestNotificationPermission()
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
      })
    }
  })

  onDestroy(() => {
    unsubscribeSSE?.()
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

<svelte:window on:paste={handleGlobalPaste} />

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
        <Terminal bind:this={terminalComponent} session={activeSession} onBack={goHome} />
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

  </div>
{/if}
