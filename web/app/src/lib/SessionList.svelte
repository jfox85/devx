<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount } from 'svelte'
  import { listSessions, deleteSession } from '../api.js'
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

  // Group sessions by project_alias, sorted alphabetically.
  // Sessions without a project go in an "" bucket rendered last.
  $: groups = (() => {
    const map = {}
    for (const s of sessions) {
      const key = s.project_alias || ''
      if (!map[key]) map[key] = []
      map[key].push(s)
    }
    return Object.entries(map).sort(([a], [b]) => {
      if (a === '') return 1
      if (b === '') return -1
      return a.localeCompare(b)
    })
  })()

  async function handleDelete(session) {
    if (!confirm(`Remove session "${session.name}"?`)) return
    try {
      await deleteSession(session.name)
      await load()
    } catch (e) {
      error = e.message
    }
  }

</script>

<div class="min-h-dvh bg-gray-950 px-3 pt-4 pb-8">
  <div>
    <div class="flex items-center justify-between mb-4">
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
      <div class="space-y-6">
        {#each groups as [project, projectSessions]}
          <div>
            <h2 class="text-xs font-semibold uppercase tracking-wider text-gray-500 mb-2 px-1">
              {project || 'Other'}
            </h2>
            <div class="grid gap-3">
              {#each projectSessions as session (session.name)}
                <SessionCard {session} onOpen={onOpenTerminal} onDelete={handleDelete} />
              {/each}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={load} />
{/if}
