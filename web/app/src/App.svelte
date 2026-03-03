<!-- web/app/src/App.svelte -->
<script>
  import { onMount } from 'svelte'
  import { isLoggedIn, login } from './api.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  import Terminal from './lib/Terminal.svelte'

  // view is only used on mobile to toggle between sessions and terminal.
  // On desktop, both panels are always visible.
  let view = 'sessions'  // 'sessions' | 'terminal'
  let activeSession = null
  let terminalComponent

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
        e.preventDefault()
        terminalComponent.handleImagePaste(item.getAsFile())
        return
      }
    }
  }

  // Re-set the auth cookie on load using the stored token. The cookie is a
  // persistent cookie but refresh on mount ensures it stays valid.
  onMount(async () => {
    const stored = localStorage.getItem('devx_token')
    if (stored) {
      try { await login(stored) } catch { /* token invalid — login screen will show */ }
    }
  })
</script>

<svelte:window on:paste={handleGlobalPaste} />

{#if !isLoggedIn()}
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
      flex flex-col flex-shrink-0
      {view === 'terminal' ? 'hidden lg:flex lg:w-72 xl:w-80' : 'flex w-full lg:w-72 xl:w-80'}
      border-r border-[#1e2d4a]
    ">
      <SessionList onOpenTerminal={openTerminal} activeSessionName={activeSession?.name} />
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
