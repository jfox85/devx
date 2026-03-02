<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listSessions, deleteSession } from '../api.js'
  import NewSessionModal from './NewSessionModal.svelte'

  export let onOpenTerminal
  export let activeSessionName = null  // set by parent for desktop highlight

  let sessions = []
  let loading = true
  let showNewSession = false
  let error = ''
  let searchQuery = ''
  let selectedIndex = 0
  let searchInputEl
  let expandedRoutes = null  // session.name whose routes are shown

  async function load() {
    loading = true
    error = ''
    try { sessions = await listSessions() }
    catch (e) { error = e.message }
    finally { loading = false }
  }

  onMount(load)

  // Flat filtered list for keyboard navigation
  $: filtered = sessions.filter(s =>
    !searchQuery || s.name.toLowerCase().includes(searchQuery.toLowerCase())
  )

  // Grouped for display
  $: groups = (() => {
    const map = {}
    for (const s of filtered) {
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

  // Reset keyboard selection when search changes
  $: { searchQuery; selectedIndex = 0 }

  function handleKeydown(e) {
    const inInput = document.activeElement?.tagName === 'INPUT'
      || document.activeElement?.tagName === 'TEXTAREA'

    if (e.key === 'ArrowDown' && !inInput) {
      e.preventDefault()
      selectedIndex = Math.min(selectedIndex + 1, filtered.length - 1)
    } else if (e.key === 'ArrowUp' && !inInput) {
      e.preventDefault()
      selectedIndex = Math.max(selectedIndex - 1, 0)
    } else if (e.key === 'Enter' && !inInput) {
      if (filtered[selectedIndex]) onOpenTerminal(filtered[selectedIndex])
    } else if (e.key === 'Escape') {
      searchQuery = ''
      searchInputEl?.blur()
    } else if (e.key === '/' && !inInput) {
      e.preventDefault()
      searchInputEl?.focus()
    }
  }

  function allRoutes(session) {
    const result = {}
    for (const [svc, url] of Object.entries(session.routes || {})) {
      result[svc] = url.startsWith('http') ? url : 'https://' + url
    }
    for (const [svc, host] of Object.entries(session.external_routes || {})) {
      result[svc] = 'https://' + host
    }
    return result
  }

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

<svelte:window on:keydown={handleKeydown} />

<div class="flex flex-col h-full bg-[#0a0e1a]">

  <!-- Header -->
  <div class="flex items-center justify-between px-3 h-10 border-b border-[#1e2d4a] flex-shrink-0">
    <span class="text-cyan-400 font-mono font-bold text-sm tracking-widest">devx</span>
    <button
      on:click={() => showNewSession = true}
      class="text-gray-500 hover:text-cyan-400 text-[11px] font-mono px-2 py-0.5 border border-[#1e2d4a] hover:border-cyan-800 rounded-none transition-colors leading-none"
    >
      [+ new]
    </button>
  </div>

  <!-- Search -->
  <div class="flex items-center px-3 h-8 border-b border-[#1e2d4a] flex-shrink-0">
    <span class="text-gray-600 font-mono text-xs mr-2 select-none">/</span>
    <input
      bind:this={searchInputEl}
      bind:value={searchQuery}
      placeholder="filter sessions…"
      class="flex-1 bg-transparent text-gray-300 text-xs font-mono outline-none placeholder-gray-700 min-w-0"
    />
    {#if searchQuery}
      <button
        on:click={() => searchQuery = ''}
        class="text-gray-600 hover:text-gray-400 text-xs font-mono ml-1"
      >×</button>
    {/if}
  </div>

  <!-- Session list -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="px-3 py-8 text-gray-700 text-xs font-mono">loading...</div>

    {:else if error}
      <div class="px-3 py-4 text-red-600 text-xs font-mono">{error}</div>

    {:else if sessions.length === 0}
      <div class="px-3 py-8 text-center">
        <p class="text-gray-700 text-xs font-mono mb-4">no active sessions</p>
        <button
          on:click={() => showNewSession = true}
          class="text-cyan-600 hover:text-cyan-400 text-xs font-mono border border-[#1e2d4a] hover:border-cyan-900 px-3 py-1.5 transition-colors"
        >
          create first session
        </button>
      </div>

    {:else if filtered.length === 0}
      <div class="px-3 py-4 text-gray-700 text-xs font-mono">no matches</div>

    {:else}
      {#each groups as [project, projectSessions]}
        <div class="pt-3 pb-1">
          {#if project}
            <div class="px-4 pb-1 text-[10px] font-mono font-bold uppercase tracking-[0.18em] text-cyan-700/60 select-none">
              {project}
            </div>
          {/if}

          {#each projectSessions as session (session.name)}
            {@const isActive = session.name === activeSessionName}
            {@const flatIdx = filtered.indexOf(session)}
            {@const isKbSelected = flatIdx === selectedIndex}
            {@const routes = allRoutes(session)}
            {@const hasRoutes = Object.keys(routes).length > 0}

            <div class="group relative">
              <!-- Session row -->
              <button
                on:click={() => onOpenTerminal(session)}
                class="
                  w-full text-left flex items-center gap-2
                  pl-5 pr-14 py-3 lg:py-2
                  font-mono text-sm lg:text-xs
                  border-l-2 transition-colors
                  {isActive
                    ? 'text-cyan-300 bg-cyan-950/30 border-cyan-500'
                    : isKbSelected
                      ? 'text-gray-200 bg-gray-800/30 border-gray-600'
                      : 'text-gray-500 hover:text-gray-200 hover:bg-[#0d1117] border-transparent'}
                "
              >
                <span class="flex-1 truncate leading-none">{session.name}</span>
                {#if session.attention_flag}
                  <span class="text-yellow-500 text-[10px] flex-shrink-0" title="needs attention">◆</span>
                {/if}
              </button>

              <!-- Action buttons: visible on hover -->
              <div class="
                absolute right-1 top-1/2 -translate-y-1/2
                hidden group-hover:flex items-center gap-0.5
                bg-[#0a0e1a] pl-2
              ">
                {#if hasRoutes}
                  <button
                    on:click|stopPropagation={() => expandedRoutes = expandedRoutes === session.name ? null : session.name}
                    class="text-[10px] font-mono text-blue-600 hover:text-blue-400 px-1.5 py-1 transition-colors"
                    title="services"
                  >svc</button>
                {/if}
                <button
                  on:click|stopPropagation={() => handleDelete(session)}
                  class="text-[10px] font-mono text-red-800 hover:text-red-500 px-1.5 py-1 transition-colors"
                  title="delete"
                >×</button>
              </div>
            </div>

            <!-- Routes inline expansion -->
            {#if expandedRoutes === session.name}
              <div class="bg-[#0d1117] border-b border-[#1e2d4a] pl-7 pr-3 py-2 space-y-1.5">
                {#each Object.entries(routes) as [svc, url]}
                  <a
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="flex items-center gap-2 text-[11px] font-mono text-gray-600 hover:text-cyan-400 transition-colors group/link"
                  >
                    <span class="text-gray-700 group-hover/link:text-cyan-600">↗</span>
                    <span class="text-gray-500">{svc}</span>
                    <span class="text-gray-700 truncate">{url.replace('https://', '')}</span>
                  </a>
                {/each}
              </div>
            {/if}
          {/each}
        </div>
      {/each}
    {/if}
  </div>

  <!-- Key hint bar (desktop only) -->
  <div class="hidden lg:flex items-center gap-4 px-3 h-7 border-t border-[#1e2d4a] text-[10px] font-mono text-gray-700 flex-shrink-0 select-none">
    <span>↑↓ nav</span>
    <span>⏎ open</span>
    <span>/ search</span>
  </div>

</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={load} />
{/if}
