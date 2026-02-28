<!-- web/app/src/App.svelte -->
<script>
  import { onMount } from 'svelte'
  import { isLoggedIn, login } from './api.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  import Terminal from './lib/Terminal.svelte'

  let view = 'sessions'  // 'sessions' | 'terminal'
  let activeSession = null

  function openTerminal(session) {
    activeSession = session
    view = 'terminal'
  }

  function goHome() {
    view = 'sessions'
    activeSession = null
  }

  // Re-set the auth cookie on load using the stored token. The cookie is a
  // browser session cookie by default, so it's cleared on browser restart even
  // though localStorage survives. Without it the terminal iframe (which can't
  // send an Authorization header) gets 401.
  onMount(async () => {
    const stored = localStorage.getItem('devx_token')
    if (stored) {
      try { await login(stored) } catch { /* token invalid — login screen will show */ }
    }
  })
</script>

{#if !isLoggedIn()}
  <Login />
{:else if view === 'sessions'}
  <SessionList onOpenTerminal={openTerminal} />
{:else if view === 'terminal' && activeSession}
  <Terminal session={activeSession} onBack={goHome} />
{/if}
