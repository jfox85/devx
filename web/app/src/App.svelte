<!-- web/app/src/App.svelte -->
<script>
  import { isLoggedIn } from './api.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  import Terminal from './lib/Terminal.svelte'

  // view is only used on mobile to toggle between sessions and terminal.
  // On desktop, both panels are always visible.
  let view = 'sessions'  // 'sessions' | 'terminal'
  let activeSession = null
  let terminalComponent

  // Reactive: determined at page load from the localStorage marker set on login.
  // The marker is non-sensitive — the actual auth token lives in an httpOnly
  // cookie. Any API 401 clears the marker and reloads to show the login screen.
  let loggedIn = isLoggedIn()

  function openTerminal(session) {
    activeSession = session
    view = 'terminal'
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
  <div class="flex h-dvh overflow-hidden bg-[#0a0e1a]">

    <!-- Session list sidebar -->
    <div class="
      flex flex-col shrink-0
      {view === 'terminal' ? 'hidden lg:flex lg:w-72 xl:w-80' : 'flex w-full lg:w-72 xl:w-80'}
      border-r border-[#1e2d4a]
    ">
      <SessionList onOpenTerminal={openTerminal} activeSessionName={activeSession?.name} onDeleteSession={goHome} />
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

  </div>
{/if}
