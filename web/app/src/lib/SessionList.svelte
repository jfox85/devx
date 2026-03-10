<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { listSessions, deleteSession } from '../api.js'
  import NewSessionModal from './NewSessionModal.svelte'

  export let onOpenTerminal
  export let activeSessionName = null  // set by parent for desktop highlight
  export let onDeleteSession = null    // called when the currently-active session is deleted

  let sessions = []
  let loading = true
  let showNewSession = false
  let error = ''
  let searchQuery = ''
  let selectedIndex = 0
  let searchFocused = false   // true while the filter input has focus
  let searchInputEl
  let expandedRoutes = null  // session.name whose routes are shown
  let pendingDelete = null   // session.name awaiting second-click confirmation

  async function load({ background = false } = {}) {
    if (!background) loading = true
    error = ''
    try { sessions = await listSessions() }
    catch (e) { error = e.message }
    finally { if (!background) loading = false }
  }

  function openNewSession() { showNewSession = true }

  const POLL_INTERVAL = 5000

  onMount(() => {
    load()

    // Poll for session changes (e.g. new sessions created in the terminal).
    // Background polls skip the loading spinner to avoid flickering the list.
    // Pauses when the tab is hidden to avoid unnecessary requests.
    let pollTimer = null
    function startPolling() {
      pollTimer = setInterval(() => { if (!document.hidden) load({ background: true }) }, POLL_INTERVAL)
    }
    function handleVisibilityChange() {
      if (!document.hidden) load({ background: true }) // immediate refresh when tab becomes visible
    }
    startPolling()
    document.addEventListener('visibilitychange', handleVisibilityChange)

    window.addEventListener('devx:focusSessionList', focusSearch)
    window.addEventListener('devx:newSession', openNewSession)
    return () => {
      clearInterval(pollTimer)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      window.removeEventListener('devx:focusSessionList', focusSearch)
      window.removeEventListener('devx:newSession', openNewSession)
    }
  })

  function focusSearch() {
    // Start nav cursor at the active session so arrows move from a known position
    const activeIdx = displayOrdered.findIndex(s => s.name === activeSessionName)
    if (activeIdx >= 0) selectedIndex = activeIdx
    searchInputEl?.focus()
    searchInputEl?.select()
  }

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

  // displayOrdered matches the visual top-to-bottom order (projects sorted
  // alphabetically, sessions in original API order within each project).
  // Use this for keyboard nav so ArrowDown moves to the visually next item.
  $: displayOrdered = groups.flatMap(([, sessions]) => sessions)

  // Reset keyboard selection when search changes
  $: { searchQuery; selectedIndex = 0 }

  // Only show the keyboard cursor highlight while the search box is in use.
  // When idle, only the cyan activeSession highlight shows — no competing states.
  $: showKbCursor = searchFocused || searchQuery.length > 0

  function selectSession(sess) {
    // Clear the filter so the full list reappears after switching
    searchQuery = ''
    selectedIndex = 0
    onOpenTerminal(sess)
  }

  function handleKeydown(e) {
    // True if focused on something other than our own search box
    const inOtherInput = (document.activeElement?.tagName === 'INPUT'
      || document.activeElement?.tagName === 'TEXTAREA')
      && document.activeElement !== searchInputEl

    // Arrow keys and Enter work even while search is focused (combobox pattern)
    if (e.key === 'ArrowDown' && !inOtherInput) {
      e.preventDefault()
      selectedIndex = Math.min(selectedIndex + 1, displayOrdered.length - 1)
    } else if (e.key === 'ArrowUp' && !inOtherInput) {
      e.preventDefault()
      selectedIndex = Math.max(selectedIndex - 1, 0)
    } else if (e.key === 'Enter' && !inOtherInput) {
      if (displayOrdered[selectedIndex]) selectSession(displayOrdered[selectedIndex])
    } else if (e.key === 'Escape') {
      searchQuery = ''
      searchInputEl?.blur()
    } else if (e.key === '/' && !inOtherInput && document.activeElement !== searchInputEl) {
      e.preventDefault()
      focusSearch()
    } else if (e.ctrlKey && e.shiftKey && (e.key === 's' || e.key === 'S')) {
      e.preventDefault()
      focusSearch()
    } else if (e.ctrlKey && e.shiftKey && (e.key === 'c' || e.key === 'C')) {
      e.preventDefault()
      if (!showNewSession) showNewSession = true
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
    // Two-click confirmation: first click arms the delete, second click fires it.
    // Click outside (blur/hover-leave) resets via the pendingDelete timeout below.
    if (pendingDelete !== session.name) {
      pendingDelete = session.name
      // Auto-reset after 3 s so the UI doesn't stay in armed state indefinitely.
      setTimeout(() => { if (pendingDelete === session.name) pendingDelete = null }, 3000)
      return
    }
    pendingDelete = null
    error = ''
    try {
      await deleteSession(session.name)
      if (session.name === activeSessionName) onDeleteSession?.()
      await load()
    } catch (e) {
      error = e.message || 'Delete failed'
    }
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="flex flex-col h-full bg-[#0a0e1a]">

  <!-- Header -->
  <div class="flex items-center justify-between px-3 h-10 border-b border-[#1e2d4a] shrink-0">
    <span class="text-cyan-400 font-mono font-bold text-sm tracking-widest">devx</span>
    <button
      on:click={() => showNewSession = true}
      class="text-gray-500 hover:text-cyan-400 text-[11px] font-mono px-2 py-0.5 border border-[#1e2d4a] hover:border-cyan-800 transition-colors leading-none"
    >
      [+ new]
    </button>
  </div>

  <!-- Search -->
  <div class="flex items-center px-3 h-8 border-b border-[#1e2d4a] shrink-0">
    <span class="text-gray-600 font-mono text-xs mr-2 select-none">/</span>
    <input
      bind:this={searchInputEl}
      bind:value={searchQuery}
      aria-label="filter sessions"
      placeholder="filter sessions…"
      on:focus={() => searchFocused = true}
      on:blur={() => searchFocused = false}
      class="flex-1 bg-transparent text-gray-300 text-xs font-mono outline-none placeholder-gray-700 min-w-0"
    />
    {#if searchQuery}
      <button
        on:click={() => { searchQuery = ''; searchInputEl?.focus() }}
        class="text-gray-600 hover:text-gray-400 text-xs font-mono ml-1"
      >×</button>
    {/if}
  </div>

  <!-- Error banner (shown above list, doesn't replace it) -->
  {#if error}
    <div class="px-3 py-1.5 bg-red-950/40 border-b border-red-900/50 text-red-400 text-[11px] font-mono flex items-center justify-between shrink-0">
      <span>{error}</span>
      <button on:click={() => error = ''} class="text-red-600 hover:text-red-400 ml-2">×</button>
    </div>
  {/if}

  <!-- Session list -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="px-3 py-8 text-gray-700 text-xs font-mono">loading...</div>

    {:else if sessions.length === 0}
      <div class="px-3 py-8 text-center">
        <!-- error is shown in the banner above; here we only reach this branch  -->
        <!-- when load succeeded but returned zero sessions.                     -->
        {#if error}
          <p class="text-gray-700 text-xs font-mono">could not load sessions</p>
        {:else}
          <p class="text-gray-700 text-xs font-mono mb-4">no active sessions</p>
          <button
            on:click={() => showNewSession = true}
            class="text-cyan-600 hover:text-cyan-400 text-xs font-mono border border-[#1e2d4a] hover:border-cyan-900 px-3 py-1.5 transition-colors"
          >
            create first session
          </button>
        {/if}
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
            {@const flatIdx = displayOrdered.indexOf(session)}
            {@const isKbSelected = flatIdx === selectedIndex}
            {@const routes = allRoutes(session)}
            {@const hasRoutes = Object.keys(routes).length > 0}

            <!-- Session row: flex container so name and actions sit side by side -->
            {@const kbHighlight = showKbCursor && isKbSelected}
            <div class="
              group flex items-stretch border-l-2 transition-colors
              {isActive
                ? 'bg-cyan-950/30 border-cyan-500'
                : kbHighlight
                  ? 'bg-gray-800/30 border-gray-600'
                  : 'hover:bg-[#0d1117] border-transparent'}
            ">
              <!-- Name (main tap target) -->
              <button
                on:click={() => onOpenTerminal(session)}
                class="
                  flex-1 text-left flex items-center gap-2
                  pl-4 pr-2 py-3 lg:py-2
                  font-mono text-sm lg:text-xs
                  min-w-0
                  {isActive ? 'text-cyan-300' : kbHighlight ? 'text-gray-200' : 'text-gray-500 hover:text-gray-200'}
                "
              >
                <span class="flex-1 truncate leading-none">{session.name}</span>
                {#if session.attention_flag}
                  <span class="text-yellow-500 text-[10px] shrink-0">◆</span>
                {/if}
              </button>

              <!-- Action buttons:
                   Mobile (< lg): always visible, generous touch targets
                   Desktop (≥ lg): invisible by default, fade in on group hover -->
              <div class="
                flex items-center gap-px pr-1
                lg:opacity-0 lg:group-hover:opacity-100 lg:transition-opacity
              ">
                {#if hasRoutes}
                  <button
                    on:click={() => expandedRoutes = expandedRoutes === session.name ? null : session.name}
                    class="
                      font-mono
                      text-blue-600 hover:text-blue-300 active:text-blue-200
                      text-sm lg:text-[10px]
                      px-3 lg:px-1.5 py-4 lg:py-1.5
                      transition-colors
                    "
                    title="services"
                  >svc</button>
                {/if}
                <button
                  on:click={() => handleDelete(session)}
                  class="
                    font-mono
                    {pendingDelete === session.name
                      ? 'text-red-400'
                      : 'text-red-700 hover:text-red-400 active:text-red-300'}
                    text-lg lg:text-[10px]
                    px-3 lg:px-1.5 py-4 lg:py-1.5
                    transition-colors
                  "
                  title={pendingDelete === session.name ? 'click again to confirm' : 'delete'}
                >{pendingDelete === session.name ? '!×' : '×'}</button>
              </div>
            </div>

            <!-- Routes inline expansion -->
            {#if expandedRoutes === session.name}
              <div class="bg-[#0d1117] border-b border-[#1e2d4a] pl-6 pr-3 py-2 space-y-1.5">
                {#each Object.entries(routes) as [svc, url]}
                  <a
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="flex items-center gap-2 text-[11px] font-mono hover:text-cyan-400 transition-colors"
                  >
                    <span class="text-gray-700">↗</span>
                    <span class="text-gray-500">{svc}</span>
                    <span class="text-gray-700 truncate">{url.replace('https://', '')}</span>
                  </a>
                {/each}
                <button
                  on:click={() => expandedRoutes = null}
                  class="text-[10px] font-mono text-gray-700 hover:text-gray-500 mt-1 transition-colors"
                >close ×</button>
              </div>
            {/if}
          {/each}
        </div>
      {/each}
    {/if}
  </div>

  <!-- Key hint bar (desktop only) -->
  <div class="hidden lg:flex items-center gap-4 px-3 h-7 border-t border-[#1e2d4a] text-[10px] font-mono text-gray-700 shrink-0 select-none">
    <span>↑↓ nav</span>
    <span>⏎ open</span>
    <span>/ search</span>
    <span class="ml-auto">^⇧C new</span>
    <span>^⇧S focus</span>
  </div>

</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={load} />
{/if}
