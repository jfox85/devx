<!-- web/app/src/lib/NewSessionModal.svelte -->
<script>
  import { onMount, createEventDispatcher } from 'svelte'
  import { createSession, listProjects } from '../api.js'

  const dispatch = createEventDispatcher()

  const LAST_PROJECT_KEY = 'devx_last_project'

  let name = ''
  let project = localStorage.getItem(LAST_PROJECT_KEY) || ''
  let projects = []
  let error = ''
  let loading = false

  onMount(async () => {
    try {
      projects = await listProjects()
      // If the remembered project is no longer in the list, clear it
      if (project && !projects.includes(project)) project = ''
      // If nothing remembered but there's only one project, pre-select it
      if (!project && projects.length === 1) project = projects[0]
    } catch { /* if we can't load projects just hide the dropdown */ }
  })

  async function handleSubmit() {
    if (!name.trim()) { error = 'Session name is required'; return }
    loading = true
    error = ''
    try {
      await createSession(name.trim(), project || undefined)
      if (project) localStorage.setItem(LAST_PROJECT_KEY, project)
      dispatch('created')
      dispatch('close')
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
</script>

<div class="fixed inset-0 bg-black/60 flex items-end sm:items-center justify-center z-50 p-4"
     role="dialog" aria-modal="true" tabindex="-1"
     on:click|self={() => dispatch('close')}
     on:keydown={(e) => e.key === 'Escape' && dispatch('close')}>
  <div class="w-full max-w-sm bg-gray-900 rounded-2xl p-6 shadow-xl">
    <h2 class="text-white font-semibold text-lg mb-4">New Session</h2>
    <form on:submit|preventDefault={handleSubmit}>
      <label for="session-name" class="block text-gray-400 text-sm mb-1">Branch / session name</label>
      <input id="session-name" bind:value={name} placeholder="feature/my-branch"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-3 text-base focus:outline-none focus:ring-2 focus:ring-blue-500" />

      {#if projects.length > 0}
        <label for="session-project" class="block text-gray-400 text-sm mb-1">Project</label>
        <select id="session-project" bind:value={project}
          class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-4 text-base focus:outline-none focus:ring-2 focus:ring-blue-500 appearance-none">
          <option value="">— none —</option>
          {#each projects as p}
            <option value={p}>{p}</option>
          {/each}
        </select>
      {/if}

      {#if error}<p class="text-red-400 text-sm mb-3">{error}</p>{/if}
      <div class="flex gap-3">
        <button type="button" on:click={() => dispatch('close')}
          class="flex-1 bg-gray-700 text-white py-3 rounded-lg font-medium hover:bg-gray-600 transition-colors">
          Cancel
        </button>
        <button type="submit" disabled={loading}
          class="flex-1 bg-blue-600 text-white py-3 rounded-lg font-semibold hover:bg-blue-500 transition-colors">
          {loading ? 'Creating...' : 'Create'}
        </button>
      </div>
    </form>
  </div>
</div>
