<!-- web/app/src/lib/NewSessionModal.svelte -->
<script>
  import { createEventDispatcher } from 'svelte'
  import { createSession } from '../api.js'

  const dispatch = createEventDispatcher()

  let name = ''
  let project = ''
  let error = ''
  let loading = false

  async function handleSubmit() {
    if (!name.trim()) { error = 'Session name is required'; return }
    loading = true
    error = ''
    try {
      await createSession(name.trim(), project.trim() || undefined)
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
      <label for="session-project" class="block text-gray-400 text-sm mb-1">Project (optional)</label>
      <input id="session-project" bind:value={project} placeholder="myproject"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-4 text-base focus:outline-none focus:ring-2 focus:ring-blue-500" />
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
