<!-- web/app/src/App.svelte -->
<script>
  import { isLoggedIn } from './api.js'
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
</script>

{#if !isLoggedIn()}
  <Login />
{:else if view === 'sessions'}
  <SessionList onOpenTerminal={openTerminal} />
{:else if view === 'terminal' && activeSession}
  <Terminal session={activeSession} onBack={goHome} />
{/if}
