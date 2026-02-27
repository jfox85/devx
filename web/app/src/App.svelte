<!-- web/app/src/App.svelte -->
<script>
  import { isLoggedIn } from './api.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'

  let view = 'sessions'
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
{:else if view === 'terminal'}
  <!-- Phase 3: Terminal component placeholder -->
  <div class="min-h-screen bg-gray-950 flex flex-col">
    <div class="flex items-center gap-3 p-3 bg-gray-900 border-b border-gray-800">
      <button on:click={goHome} class="text-gray-400 hover:text-white text-sm px-2 py-1 rounded">Back</button>
      <span class="text-white font-medium">{activeSession?.name}</span>
      <span class="text-gray-500 text-sm">{activeSession?.branch}</span>
    </div>
    <div class="flex-1 flex items-center justify-center text-gray-500">
      Terminal coming in Phase 3
    </div>
  </div>
{/if}
