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
    if (!name.trim()) { error = 'session name is required'; return }
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

<!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
<div
  class="fixed inset-0 bg-black/70 flex items-end sm:items-center justify-center z-50 p-4"
  role="dialog" aria-modal="true" tabindex="-1"
  on:click|self={() => dispatch('close')}
  on:keydown={(e) => e.key === 'Escape' && dispatch('close')}
>
  <div class="w-full max-w-sm bg-[#0d1117] border border-[#1e2d4a]">
    <!-- Modal title bar -->
    <div class="flex items-center justify-between px-4 py-2 border-b border-[#1e2d4a]">
      <span class="text-cyan-400 text-xs font-mono font-bold tracking-widest">new session</span>
      <button
        on:click={() => dispatch('close')}
        class="text-gray-600 hover:text-gray-400 font-mono text-xs"
      >×</button>
    </div>

    <form on:submit|preventDefault={handleSubmit} class="p-4 space-y-3">
      <div>
        <label for="session-name" class="block text-gray-600 text-[11px] font-mono mb-1">
          branch / session name
        </label>
        <input
          id="session-name"
          bind:value={name}
          placeholder="feature/my-branch"
          autofocus
          class="
            w-full bg-transparent border border-[#1e2d4a] focus:border-cyan-800
            text-gray-300 text-xs font-mono px-3 py-2
            outline-none transition-colors placeholder-gray-700
          "
        />
      </div>

      {#if projects.length > 0}
        <div>
          <label for="session-project" class="block text-gray-600 text-[11px] font-mono mb-1">
            project
          </label>
          <select
            id="session-project"
            bind:value={project}
            class="
              w-full bg-[#0a0e1a] border border-[#1e2d4a] focus:border-cyan-800
              text-gray-300 text-xs font-mono px-3 py-2
              outline-none transition-colors appearance-none
            "
          >
            <option value="">— none —</option>
            {#each projects as p}
              <option value={p}>{p}</option>
            {/each}
          </select>
        </div>
      {/if}

      {#if error}
        <p class="text-red-500 text-xs font-mono">{error}</p>
      {/if}

      <div class="flex gap-2 pt-1">
        <button
          type="button"
          on:click={() => dispatch('close')}
          class="flex-1 border border-[#1e2d4a] hover:border-gray-600 text-gray-600 hover:text-gray-400 py-2 text-xs font-mono transition-colors"
        >cancel</button>
        <button
          type="submit"
          disabled={loading}
          class="flex-1 border border-cyan-900 hover:border-cyan-700 text-cyan-500 hover:text-cyan-300 py-2 text-xs font-mono transition-colors disabled:opacity-40"
        >{loading ? 'creating...' : '[ create ]'}</button>
      </div>
    </form>
  </div>
</div>
