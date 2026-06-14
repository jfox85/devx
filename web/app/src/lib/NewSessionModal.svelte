<!-- web/app/src/lib/NewSessionModal.svelte -->
<script>
  import { onMount, createEventDispatcher } from 'svelte'
  import { createSession, getSettings, listProjects } from '../api.js'

  const dispatch = createEventDispatcher()

  const LAST_PROJECT_KEY = 'devx_last_project'

  let name = ''
  let project = localStorage.getItem(LAST_PROJECT_KEY) || ''
  let target = 'host'
  let defaultTarget = 'host'
  let projects = []
  let error = ''
  let projectLoadError = ''
  let projectsLoading = true
  let loading = false
  let nameInputEl

  onMount(async () => {
    // Explicitly focus the name field — autofocus alone fails when an iframe held focus
    nameInputEl?.focus()
    try {
      const settings = await getSettings()
      defaultTarget = settings.default_session_target || 'host'
      if (['host', 'gatepost', 'docker'].includes(defaultTarget)) target = defaultTarget
    } catch { /* settings are optional; keep fallback */ }
    try {
      projects = await listProjects()
      // If the remembered project is no longer in the list, clear it
      if (project && !projects.includes(project)) project = ''
      // If nothing remembered but there's only one project, pre-select it
      if (!project && projects.length === 1) project = projects[0]
    } catch (e) {
      projectLoadError = e.message || 'could not load projects'
    } finally {
      projectsLoading = false
    }
  })

  async function handleSubmit() {
    if (!name.trim()) { error = 'session name is required'; return }
    loading = true
    error = ''
    try {
      await createSession(name.trim(), project || undefined, target || undefined)
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
  <div class="w-full max-w-sm max-h-[90dvh] overflow-y-auto bg-[#0d1117] border border-[#1e2d4a]">
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
          bind:this={nameInputEl}
          bind:value={name}
          placeholder="feature/my-branch"
          class="
            w-full bg-transparent border border-[#1e2d4a] focus:border-cyan-800
            text-gray-300 text-xs font-mono px-3 py-2
            outline-none transition-colors placeholder-gray-700
          "
        />
      </div>

      <div>
        <div class="flex items-center justify-between mb-1">
          <span class="block text-gray-600 text-[11px] font-mono">session type</span>
          <span class="text-gray-700 text-[10px] font-mono">default: {defaultTarget}</span>
        </div>
        <div class="grid grid-cols-2 gap-2" role="radiogroup" aria-label="session type">
          {#each [
            ['host', 'host'],
            ['gatepost', 'gatepost'],
            ['docker', 'docker'],
          ] as [value, label]}
            <label class="flex items-center gap-2 border border-[#1e2d4a] px-2 py-2 text-[11px] font-mono cursor-pointer transition-colors {target === value ? 'text-cyan-300 border-cyan-800 bg-cyan-950/20' : 'text-gray-500 hover:text-gray-300 hover:border-gray-700'}">
              <input
                type="radio"
                name="session-target"
                value={value}
                bind:group={target}
                class="accent-cyan-600"
              />
              <span>{label}</span>
            </label>
          {/each}
        </div>
      </div>

      <div>
        <label for="session-project" class="block text-gray-600 text-[11px] font-mono mb-1">
          project
        </label>
        <div class="relative">
          <select
            id="session-project"
            bind:value={project}
            disabled={projectsLoading || projects.length === 0}
            on:keydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleSubmit() } }}
            class="
              w-full bg-[#0a0e1a] border border-[#1e2d4a] focus:border-cyan-800
              text-gray-300 text-xs font-mono px-3 py-2 pr-7
              outline-none transition-colors appearance-none disabled:opacity-50
            "
          >
            {#if projectsLoading}
              <option value="">loading projects…</option>
            {:else if projects.length === 0}
              <option value="">no registered projects found</option>
            {:else}
              <option value="">default / current directory</option>
              {#each projects as p}
                <option value={p}>{p}</option>
              {/each}
            {/if}
          </select>
          <span class="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-600 text-[10px]">▾</span>
        </div>
        {#if projectLoadError}
          <p class="mt-1 text-yellow-600 text-[10px] font-mono leading-relaxed">
            Could not load projects: {projectLoadError}
          </p>
        {/if}
      </div>

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
