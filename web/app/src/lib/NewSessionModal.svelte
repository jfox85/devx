<!-- web/app/src/lib/NewSessionModal.svelte -->
<script>
  import { onMount, createEventDispatcher } from 'svelte'
  import { createSession, getSettings, listProjects } from '../api.js'

  const dispatch = createEventDispatcher()

  const LAST_PROJECT_KEY = 'devx_last_project'

  let name = ''
  let project = localStorage.getItem(LAST_PROJECT_KEY) || ''
  let targetOverride = ''
  let defaultTarget = 'host'
  let projects = []
  let projectTargets = {}
  let projectsLoading = true
  let projectLoadError = ''
  let error = ''
  let loading = false
  let progress = []
  let nameInputEl
  let modalEl

  $: effectiveDefaultTarget = projectTargets[project] || defaultTarget
  $: selectedTarget = targetOverride || effectiveDefaultTarget

  onMount(async () => {
    // Explicitly focus the name field — autofocus alone fails when an iframe held focus.
    // Retry after layout because desktop Wails/ttyd can reclaim focus briefly.
    focusName()
    setTimeout(focusName, 0)
    setTimeout(focusName, 80)
    try {
      const settings = await getSettings()
      defaultTarget = settings.default_session_target || 'host'
    } catch { /* settings are optional; keep fallback */ }
    try {
      const projectData = await listProjects()
      projects = projectData.projects || []
      projectTargets = projectData.projectTargets || {}
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
    progress = []
    try {
      const created = await createSession(name.trim(), project || undefined, {
        target: selectedTarget,
        onProgress: (msgs) => { progress = msgs }
      })
      if (project) localStorage.setItem(LAST_PROJECT_KEY, project)
      dispatch('created', created)
      dispatch('close')
    } catch (e) {
      error = e.message
    } finally {
      loading = false
      progress = []
    }
  }

  function focusName() {
    nameInputEl?.focus()
    nameInputEl?.select()
  }

  function focusableControls() {
    return Array.from(modalEl?.querySelectorAll('input, select, button') || [])
      .filter(el => !el.disabled && el.offsetParent !== null)
  }

  function handleModalKeydown(e) {
    if (e.key === 'Escape') {
      dispatch('close')
      return
    }
    if (e.key === 'Enter' && e.target?.tagName !== 'SELECT' && e.target?.tagName !== 'TEXTAREA') {
      e.preventDefault()
      if (!loading) handleSubmit()
      return
    }
    if (e.key !== 'Tab') return
    const controls = focusableControls()
    if (controls.length === 0) return
    const first = controls[0]
    const last = controls[controls.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
</script>

<!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
<div
  bind:this={modalEl}
  class="fixed inset-0 bg-black/70 flex items-end sm:items-center justify-center z-50 p-4"
  role="dialog" aria-modal="true" tabindex="-1"
  on:click|self={() => dispatch('close')}
  on:keydown={handleModalKeydown}
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

      <div>
        <div class="flex items-center justify-between mb-1">
          <span class="block text-gray-600 text-[11px] font-mono">session type</span>
          <span class="text-gray-700 text-[10px] font-mono">default: {effectiveDefaultTarget}</span>
        </div>
        <div class="grid grid-cols-3 gap-2" role="radiogroup" aria-label="session type">
          {#each [
            ['host', 'host'],
            ['gatepost', 'gatepost'],
            ['docker', 'docker'],
          ] as [value, label]}
            <label class="flex items-center gap-2 border border-[#1e2d4a] px-2 py-2 text-[11px] font-mono cursor-pointer transition-colors {selectedTarget === value ? 'text-cyan-300 border-cyan-800 bg-cyan-950/20' : 'text-gray-500 hover:text-gray-300 hover:border-gray-700'}">
              <input
                type="radio"
                name="session-target"
                value={value}
                checked={selectedTarget === value}
                on:change={() => { targetOverride = value }}
                class="accent-cyan-600"
              />
              <span>{label}</span>
            </label>
          {/each}
        </div>
      </div>

      {#if progress.length > 0}
        <div class="border border-[#1e2d4a] bg-[#080c14] p-2 max-h-24 overflow-y-auto">
          {#each progress as msg}
            <p class="text-gray-500 text-[10px] font-mono leading-relaxed">{msg}</p>
          {/each}
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
