<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount } from 'svelte'
  import { listSessions, deleteSession, flagSession } from '../api.js'
  import SessionCard from './SessionCard.svelte'
  import NewSessionModal from './NewSessionModal.svelte'

  export let onOpenTerminal

  let sessions = []
  let loading = true
  let showNewSession = false
  let error = ''

  async function load() {
    loading = true
    error = ''
    try {
      sessions = await listSessions()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  onMount(load)

  async function handleDelete(session) {
    if (!confirm(`Remove session "${session.name}"?`)) return
    await deleteSession(session.name)
    await load()
  }

  async function handleFlag(session) {
    await flagSession(session.name)
    await load()
  }
</script>

<div class="min-h-screen bg-gray-950 p-4 pb-20">
  <div class="max-w-2xl mx-auto">
    <div class="flex items-center justify-between mb-6">
      <h1 class="text-2xl font-bold text-white">devx</h1>
      <button on:click={() => showNewSession = true}
        class="bg-blue-600 hover:bg-blue-500 text-white font-medium px-4 py-2 rounded-lg text-sm transition-colors">
        + New Session
      </button>
    </div>

    {#if loading}
      <p class="text-gray-400 text-center py-12">Loading sessions...</p>
    {:else if error}
      <p class="text-red-400 text-center py-12">{error}</p>
    {:else if sessions.length === 0}
      <p class="text-gray-500 text-center py-12">No active sessions. Create one to get started.</p>
    {:else}
      <div class="grid gap-3">
        {#each sessions as session (session.name)}
          <SessionCard {session} onOpen={onOpenTerminal} onDelete={handleDelete} onFlag={handleFlag} />
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={load} />
{/if}
