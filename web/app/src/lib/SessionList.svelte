<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount, tick } from 'svelte'
  import { listSessionsWithSummary, getStaleSummary, deleteSession, renameSession, prewarmTerminal, pruneStaleCleanSessions, markSessionReviewed, colorSession, pinSession, unpinSession } from '../api.js'
  import { markPrewarmed, markSwitchStart } from './stores/sessionUiState.js'
  import NewSessionModal from './NewSessionModal.svelte'
  import StaleReviewPanel from './StaleReviewPanel.svelte'
  import { buildSessionSections, loadSessionView, saveSessionView, relativeActivity } from './sessionOrdering.js'
  import { openExternal as openExternalDesktop } from './desktopBridge.js'

  export let onOpenTerminal
  export let activeSessionName = null  // set by parent for desktop highlight
  export let onDeleteSession = null    // called when the currently-active session is deleted
  export let refreshTrigger = 0        // bump to force an immediate background reload
  export let flashSession = null       // session name to momentarily highlight

  let sessions = []
  let staleSummary = null
  let staleReviewSummary = null
  let loading = true
  let showNewSession = false
  let error = ''
  let searchQuery = ''
  let selectedSessionName = null
  let searchFocused = false   // true while the filter input has focus
  let searchInputEl
  let expandedRoutes = null  // session.name whose routes are shown
  let pendingDelete = null   // session.name awaiting second-click confirmation
  let deletingSessions = {}  // session.name -> true while backend deletion is running
  let cleanupMessage = ''
  let editingName = null     // session.name being renamed
  let editValue = ''         // current text input value
  let showStaleReview = false
  let staleReviewLoading = false
  let pruningStale = false
  let pendingPruneStale = false
  let showStatusHelp = false
  let sessionView = loadSessionView()
  let pendingPins = {}
  let liveMessage = ''
  let activityNow = Date.now()

  // Hover/focus prewarm with a short debounce so list scanning doesn't fire a
  // request per row. Prewarm is read-only with respect to tmux (it only starts
  // ttyd for sessions whose tmux already exists) and the backend enforces a
  // global cap, so over-triggering is bounded and harmless.
  const PREWARM_DEBOUNCE_MS = 150
  let prewarmTimer = null
  let prewarmRequested = new Set()

  function schedulePrewarm(name) {
    clearTimeout(prewarmTimer)
    prewarmTimer = setTimeout(() => firePrewarm(name), PREWARM_DEBOUNCE_MS)
  }

  function cancelPrewarm() {
    clearTimeout(prewarmTimer)
    prewarmTimer = null
  }

  function firePrewarm(name) {
    if (prewarmRequested.has(name)) return
    prewarmRequested.add(name)
    prewarmTerminal(name)
      .then(status => markPrewarmed(name, !!status?.ready))
      .catch(() => { prewarmRequested.delete(name) })
    // Allow re-prewarm after the idle timeout would have reaped the instance.
    setTimeout(() => prewarmRequested.delete(name), 60_000)
  }

  const colorMap = {
    red: '#ef4444', blue: '#3b82f6', green: '#22c55e', yellow: '#eab308',
    purple: '#a855f7', orange: '#f97316', pink: '#ec4899', cyan: '#06b6d4', gray: '#64748b',
  }
  const sessionColorPalette = ['red', 'blue', 'green', 'yellow', 'purple', 'orange', 'pink', 'cyan']

  async function cycleSessionColor(e, session) {
    e.stopPropagation()
    const current = session.color || 'blue'
    const index = sessionColorPalette.indexOf(current)
    const next = sessionColorPalette[(index + 1) % sessionColorPalette.length]
    try {
      await colorSession(session.name, next)
      await load({ background: true })
    } catch (err) {
      error = err.message || 'Color change failed'
    }
  }
  function startRename(session) {
    editingName = session.name
    editValue = session.display_name || session.name
  }

  async function submitRename(session) {
    const newName = editValue.trim()
    try {
      if (newName === '' || newName === session.name) {
        await renameSession(session.name, null)  // clear
      } else {
        await renameSession(session.name, newName)
      }
      await load({ background: true })
    } catch (e) {
      error = e.message
    }
    editingName = null
  }

  function cancelRename() {
    editingName = null
  }

  let loadRequestID = 0
  async function load({ background = false } = {}) {
    const requestID = ++loadRequestID
    if (!background) loading = true
    error = ''
    try {
      const data = await listSessionsWithSummary()
      if (requestID !== loadRequestID) return
      sessions = data.sessions || []
      staleSummary = data.stale_summary || null
      if (!showStaleReview) staleReviewSummary = null
    }
    catch (e) { if (requestID === loadRequestID) error = e.message }
    finally { if (requestID === loadRequestID) loading = false }
  }

  function openNewSession() { showNewSession = true }

  const POLL_INTERVAL = 5000

  onMount(() => {
    load()

    // Poll for session changes (e.g. new sessions created in the terminal).
    // Background polls skip the loading spinner to avoid flickering the list.
    // Pauses when the tab is hidden to avoid unnecessary requests.
    let pollTimer = null
    const activityTimer = setInterval(() => { activityNow = Date.now() }, 60_000)
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
      clearInterval(activityTimer)
      clearTimeout(prewarmTimer)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      window.removeEventListener('devx:focusSessionList', focusSearch)
      window.removeEventListener('devx:newSession', openNewSession)
    }
  })

  function focusSearch() {
    // Start nav cursor at the active session so arrows move from a known position
    if (displayOrdered.some(s => s.name === activeSessionName)) selectedSessionName = activeSessionName
    searchInputEl?.focus()
    searchInputEl?.select()
  }

  $: sections = buildSessionSections(sessions, searchQuery, sessionView)
  $: displayOrdered = sections.flatMap(section => section.sessions)
  $: selectedIndex = Math.max(0, displayOrdered.findIndex(s => s.name === selectedSessionName))

  // Immediate background reload when parent bumps refreshTrigger (e.g. on SSE flag event)
  let _prevTrigger = refreshTrigger
  $: if (refreshTrigger !== _prevTrigger) { _prevTrigger = refreshTrigger; load({ background: true }) }

  // Keep keyboard selection keyed by session identity as filtering/reordering occurs.
  $: if (displayOrdered.length && !displayOrdered.some(s => s.name === selectedSessionName)) {
    selectedSessionName = displayOrdered[0].name
  }

  // Only show the keyboard cursor highlight while the search box is in use.
  // When idle, only the cyan activeSession highlight shows — no competing states.
  $: showKbCursor = searchFocused || searchQuery.length > 0

  function selectSession(sess) {
    markSwitchStart(sess.name)
    // Clear the filter so the full list reappears after switching
    searchQuery = ''
    selectedSessionName = sess.name
    onOpenTerminal(sess)
  }

  function handleKeydown(e) {
    // True if focused on something other than our own search box
    const inOtherInput = (document.activeElement?.tagName === 'INPUT'
      || document.activeElement?.tagName === 'TEXTAREA')
      && document.activeElement !== searchInputEl
    const inOtherControl = !!e.target?.closest?.('button, a, select, [contenteditable="true"]')

    // Arrow keys and Enter work even while search is focused (combobox pattern),
    // but never override a focused row/view/action control.
    if (e.key === 'ArrowDown' && !inOtherInput && !inOtherControl) {
      e.preventDefault()
      const next = displayOrdered[Math.min(selectedIndex + 1, displayOrdered.length - 1)]
      if (next) { selectedSessionName = next.name; schedulePrewarm(next.name) }
    } else if (e.key === 'ArrowUp' && !inOtherInput && !inOtherControl) {
      e.preventDefault()
      const next = displayOrdered[Math.max(selectedIndex - 1, 0)]
      if (next) { selectedSessionName = next.name; schedulePrewarm(next.name) }
    } else if (e.key === 'Enter' && !inOtherInput && !inOtherControl) {
      if (displayOrdered[selectedIndex]) selectSession(displayOrdered[selectedIndex])
    } else if (e.shiftKey && (e.key === 'p' || e.key === 'P') && !inOtherInput && !inOtherControl && document.activeElement !== searchInputEl && document.activeElement?.tagName !== 'SELECT') {
      e.preventDefault()
      if (displayOrdered[selectedIndex]) handlePin(displayOrdered[selectedIndex], true)
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

  function setSessionView(value) {
    sessionView = value
    saveSessionView(localStorage, value)
  }

  async function handlePin(session, restorePinFocus = false) {
    if (pendingPins[session.name]) return
    liveMessage = ''
    await tick()
    const nextPinned = !session.pinned
    // Supersede any poll that started before this mutation so stale server data
    // cannot overwrite the optimistic state.
    loadRequestID++
    selectedSessionName = session.name
    pendingPins = { ...pendingPins, [session.name]: true }
    sessions = sessions.map(item => item.name === session.name ? { ...item, pinned: nextPinned } : item)
    try {
      if (nextPinned) await pinSession(session.name)
      else await unpinSession(session.name)
      liveMessage = `${nextPinned ? 'Pinned' : 'Unpinned'} ${session.display_name || session.name}`
      await load({ background: true })
    } catch (e) {
      sessions = sessions.map(item => item.name === session.name ? { ...item, pinned: !nextPinned } : item)
      error = e.message || `Couldn't ${nextPinned ? 'pin' : 'unpin'} session`
    } finally {
      const next = { ...pendingPins }
      delete next[session.name]
      pendingPins = next
      if (restorePinFocus) {
        await tick()
        const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(session.name) : session.name
        document.querySelector(`[data-pin-session="${escaped}"]`)?.focus()
      }
    }
  }

  function allRoutes(session) {
    const result = {}
    // Prefer externally reachable tunnel routes. Local Caddy routes are only a
    // fallback for services without a Cloudflare/external URL.
    for (const [svc, host] of Object.entries(session.external_routes || {})) {
      result[svc] = host.startsWith('http') ? host : 'https://' + host
    }
    for (const [svc, url] of Object.entries(session.routes || {})) {
      if (result[svc]) continue
      result[svc] = url.startsWith('http') ? url : 'https://' + url
    }
    return result
  }

  function openExternal(e, url) {
    // In the desktop shell, route through the native host so the link opens in
    // the user's real browser instead of inside the WebView. In a browser,
    // openExternalDesktop returns false and we leave the default link behaviour.
    if (openExternalDesktop(url, (err) => { error = err?.message || String(err) })) {
      e.preventDefault()
    }
  }

  async function handleCreated(event) {
    const created = event.detail || {}
    await load({ background: true })
    const sess = sessions.find(s => s.name === created.name) || created
    if (sess?.name) selectSession(sess)
  }

  async function handleDelete(session) {
    if (deletingSessions[session.name]) return false
    // Two-click confirmation: first click arms the delete, second click fires it.
    // Click outside (blur/hover-leave) resets via the pendingDelete timeout below.
    if (pendingDelete !== session.name) {
      pendingDelete = session.name
      cleanupMessage = `Click again to delete ${session.display_name || session.name}`
      // Auto-reset after 3 s so the UI doesn't stay in armed state indefinitely.
      setTimeout(() => {
        if (pendingDelete === session.name) {
          pendingDelete = null
          cleanupMessage = ''
        }
      }, 3000)
      return false
    }
    pendingDelete = null
    error = ''
    cleanupMessage = `Removing ${session.display_name || session.name}…`
    deletingSessions = { ...deletingSessions, [session.name]: true }
    const deletedIndex = displayOrdered.findIndex(item => item.name === session.name)
    const wasSelected = selectedSessionName === session.name
    try {
      await deleteSession(session.name)
      cleanupMessage = `Removed ${session.display_name || session.name}; refreshing…`
      if (session.name === activeSessionName) onDeleteSession?.()
      await load()
      await tick()
      if (wasSelected && displayOrdered.length) {
        selectedSessionName = displayOrdered[Math.min(Math.max(deletedIndex, 0), displayOrdered.length - 1)].name
      }
      cleanupMessage = ''
      return true
    } catch (e) {
      error = e.message || 'Delete failed'
      cleanupMessage = ''
      return false
    } finally {
      const next = { ...deletingSessions }
      delete next[session.name]
      deletingSessions = next
    }
  }

  $: sessionByName = Object.fromEntries(sessions.map(s => [s.name, s]))
  $: reviewStatuses = staleReviewSummary?.statuses || []
  $: staleCleanStatuses = reviewStatuses.filter(s => s.category === 'stale-clean')
  $: staleNeedsReviewStatuses = reviewStatuses.filter(s => s.category === 'stale-needs-review' || s.category === 'broken')
  $: staleReviewCounts = staleReviewSummary || staleSummary || { clean: 0, needs_review: 0, broken: 0 }
  $: staleDisplayStatuses = [...staleCleanStatuses, ...staleNeedsReviewStatuses]
  $: hasStaleSessions = (staleReviewCounts.clean || 0) > 0 || (staleReviewCounts.needs_review || 0) > 0 || (staleReviewCounts.broken || 0) > 0















  function statusTitle(session) {
    const reasons = session.status?.reasons || session.stale?.reasons || []
    return [session.status?.label, ...reasons].filter(Boolean).join(': ')
  }

  function targetLabel(session) {
    if (session.gatepost?.enabled) return session.gatepost.bypass ? 'gatepost bypass' : 'gatepost'
    if (session.target_type === 'docker') return 'docker'
    if (session.target_type === 'gatepost') return 'gatepost'
    return 'host'
  }

  async function loadStaleReview() {
    staleReviewLoading = true
    error = ''
    try {
      const data = await getStaleSummary(staleSummary?.threshold_days)
      staleReviewSummary = data.stale_summary || null
      pendingPruneStale = false
    } catch (e) {
      error = e.message || 'Failed to load stale review'
    } finally {
      staleReviewLoading = false
    }
  }

  async function toggleStaleReview() {
    showStaleReview = !showStaleReview
    if (showStaleReview && !staleReviewSummary) await loadStaleReview()
  }

  async function handleMarkReviewed(status) {
    error = ''
    try {
      await markSessionReviewed(status.session_name)
      await load({ background: true })
      await loadStaleReview()
    } catch (e) {
      error = e.message || 'Failed to mark reviewed'
    }
  }

  async function handleDeleteStaleStatus(status) {
    const session = sessionByName[status.session_name] || { name: status.session_name }
    const deleted = await handleDelete(session)
    if (deleted) await loadStaleReview()
  }

  function handleRepairStaleStatus(status) {
    const session = sessionByName[status.session_name] || { name: status.session_name }
    onOpenTerminal(session)
  }

  async function handlePruneStaleClean() {
    if (staleCleanStatuses.length === 0 || pruningStale) return
    if (!pendingPruneStale) {
      pendingPruneStale = true
      setTimeout(() => { if (pendingPruneStale) pendingPruneStale = false }, 6000)
      return
    }
    pruningStale = true
    pendingPruneStale = false
    error = ''
    try {
      await pruneStaleCleanSessions(staleReviewSummary?.threshold_days || staleSummary?.threshold_days)
      if (staleCleanStatuses.some(s => s.session_name === activeSessionName)) onDeleteSession?.()
      await load()
      await loadStaleReview()
      pendingPruneStale = false
      showStaleReview = staleNeedsReviewStatuses.length > 0
    } catch (e) {
      error = e.message || 'Stale cleanup failed'
    } finally {
      pruningStale = false
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
        aria-label="Clear search"
        class="text-gray-600 hover:text-gray-400 text-xs font-mono ml-1"
      >×</button>
    {/if}
  </div>

  <!-- Session organization -->
  <div role="group" class="flex items-center gap-1 px-3 h-12 lg:h-8 border-b border-[#1e2d4a] shrink-0" aria-label="Session view">
    <span class="text-[10px] font-mono text-gray-700 mr-1">view</span>
    {#each [['recent', 'Recent'], ['projects', 'Projects']] as [value, label]}
      <button
        type="button"
        on:click={() => setSessionView(value)}
        aria-pressed={sessionView === value}
        disabled={loading}
        class="px-3 lg:px-2 min-h-11 lg:min-h-0 lg:h-6 text-[11px] lg:text-[10px] font-mono rounded-sm border transition-colors
          {sessionView === value ? 'text-cyan-300 border-cyan-800 bg-cyan-950/30' : 'text-gray-600 border-transparent hover:text-gray-300 hover:border-gray-800'}"
      >{label}</button>
    {/each}
    <span class="ml-auto text-[9px] font-mono text-gray-700">{sessionView === 'recent' ? 'across projects' : 'grouped'}</span>
  </div>

  <div class="sr-only" aria-live="polite">{liveMessage}</div>

  <!-- Error banner (shown above list, doesn't replace it) -->
  {#if error}
    <div role="alert" class="px-3 py-1.5 bg-red-950/40 border-b border-red-900/50 text-red-400 text-[11px] font-mono flex items-center justify-between shrink-0">
      <span>{error}</span>
      <span class="flex items-center gap-2 ml-2">
        <button on:click={() => load({ background: sessions.length > 0 })} class="text-red-300 hover:text-white underline">Retry</button>
        <button on:click={() => error = ''} class="text-red-600 hover:text-red-400">×</button>
      </span>
    </div>
  {/if}

  {#if cleanupMessage}
    <div class="px-3 py-1.5 bg-cyan-950/20 border-b border-cyan-900/40 text-cyan-400 text-[11px] font-mono shrink-0">
      <span class="inline-block animate-pulse mr-1">●</span>{cleanupMessage}
    </div>
  {/if}

  <!-- Stale session summary -->
  {#if hasStaleSessions}
    <div class="border-b border-[#1e2d4a] bg-amber-950/15 shrink-0">
      <button
        on:click={toggleStaleReview}
        class="w-full px-3 py-2 text-left text-[11px] font-mono text-amber-300 hover:bg-amber-950/25 flex items-center gap-2"
      >
        <span>🧹</span>
        <span class="flex-1">
          {#if staleReviewSummary}
            {staleReviewCounts.clean || 0} cleanup candidates · {(staleReviewCounts.needs_review || 0) + (staleReviewCounts.broken || 0)} need review
          {:else}
            {(staleSummary?.clean || 0) + (staleSummary?.needs_review || 0) + (staleSummary?.broken || 0)} stale/idle sessions · expand to scan
          {/if}
        </span>
        {#if staleReviewSummary && (staleReviewCounts.clean || 0) > 0}
          <span class="text-amber-500">clean up available</span>
        {/if}
        <span class="text-amber-600">{showStaleReview ? '−' : '+'}</span>
      </button>
      {#if showStaleReview}
        <StaleReviewPanel
          {staleReviewLoading}
          {staleCleanStatuses}
          {staleNeedsReviewStatuses}
          {staleDisplayStatuses}
          {sessionByName}
          {pendingPruneStale}
          {pruningStale}
          {pendingDelete}
          {deletingSessions}
          onPrune={handlePruneStaleClean}
          onOpen={handleRepairStaleStatus}
          onReviewed={handleMarkReviewed}
          onDelete={handleDeleteStaleStatus}
        />
      {/if}
    </div>
  {/if}

  <!-- Session list -->
  <div class="flex-1 overflow-y-auto" aria-busy={loading}>
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

    {:else if displayOrdered.length === 0}
      <div class="px-3 py-4 text-gray-700 text-xs font-mono">
        <p>No sessions match “{searchQuery}”</p>
        <button on:click={() => searchQuery = ''} class="mt-2 text-cyan-600 hover:text-cyan-300">Clear search</button>
      </div>

    {:else}
      {#each sections as section (section.key)}
        <div role="list" aria-label={section.label} class="pt-3 pb-1">
          <div aria-hidden="true" class="px-4 pb-1 text-[10px] font-mono font-bold uppercase tracking-[0.18em] select-none {section.kind === 'pinned' ? 'text-cyan-400' : 'text-cyan-700/60'}">
            {section.kind === 'pinned' ? '● ' : ''}{section.label}
          </div>

          {#each section.sessions as session (session.name)}
            {@const isActive = session.name === activeSessionName}
            {@const flatIdx = displayOrdered.indexOf(session)}
            {@const isKbSelected = flatIdx === selectedIndex}
            {@const routes = allRoutes(session)}
            {@const hasRoutes = Object.keys(routes).length > 0}

            <!-- Session row: flex container so name and actions sit side by side -->
            {@const kbHighlight = showKbCursor && isKbSelected}
            {@const isFlashing = session.name === flashSession}
            {@const stacked = section.showProject || section.showActivity}
            <div
              class="
                group flex items-stretch border-l-2 transition-colors
                {isActive
                  ? 'bg-cyan-950/30 border-cyan-500'
                  : kbHighlight
                    ? 'bg-gray-800/30 border-gray-600'
                    : 'hover:bg-[#0d1117] border-transparent'}
              "
              class:flag-flash={isFlashing}
              role="listitem"
            >
              <!-- Name row: color dot + name/rename + attention flag -->
              <div
                class="
                  flex-1 flex min-w-0
                  {stacked ? 'flex-col gap-1 lg:gap-0.5 pl-4 pr-1 py-0 lg:py-1.5' : 'items-center gap-2 min-h-11 lg:min-h-0 pl-4 pr-1 py-0 lg:pr-2 lg:py-2'}
                  font-mono text-sm lg:text-xs
                  {isActive ? 'text-cyan-300' : kbHighlight ? 'text-gray-200' : 'text-gray-500 hover:text-gray-200'}
                "
              >
                <!-- Recent/pinned rows stack identity above metadata because the sidebar
                     is always narrow (~288-320px on desktop, full width on mobile) and
                     those rows carry project + activity chips. Project-view rows have
                     little metadata, so they keep the compact single-line layout. -->
                <div class="{stacked ? 'flex items-center gap-2 min-h-11 min-w-0 lg:min-h-0' : 'contents'}">
                <!-- Status dot -->
                <span
                  class="shrink-0 text-[10px]"
                  style="color: {colorMap[session.status?.color] || colorMap.gray}"
                  title={statusTitle(session)}
                >●</span>
                <button
                  on:click={(e) => cycleSessionColor(e, session)}
                  class="hidden lg:block shrink-0 w-2.5 h-2.5 rounded-sm border border-black/40"
                  style="background-color: {colorMap[session.color] || colorMap.blue}"
                  title={`Session color: ${session.color || 'blue'} (click to change)`}
                  aria-label={`change color for ${session.name}`}
                ></button>

                {#if editingName === session.name}
                  <input
                    bind:value={editValue}
                    on:click|stopPropagation
                    on:keydown|stopPropagation={(e) => {
                      if (e.key === 'Enter') { e.target.blur(); submitRename(session) }
                      else if (e.key === 'Escape') cancelRename()
                    }}
                    on:blur={() => {
                      setTimeout(() => { if (editingName === session.name) cancelRename() }, 0)
                    }}
                    class="flex-1 bg-transparent text-gray-200 text-sm lg:text-xs font-mono outline-none border-b border-cyan-800 min-w-0"
                    autofocus
                  />
                {:else}
                  <button
                    type="button"
                    class="self-stretch flex flex-1 items-center min-w-0 truncate leading-none cursor-pointer text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-cyan-500"
                    on:pointerenter={() => schedulePrewarm(session.name)}
                    on:pointerleave={cancelPrewarm}
                    on:pointerdown={() => firePrewarm(session.name)}
                    on:click={() => selectSession(session)}
                    on:dblclick|stopPropagation={() => startRename(session)}
                    title="click to open, double-click to rename"
                  >
                    {session.display_name || session.name}
                    {#if session.display_name && session.display_name !== session.name}
                      <span class="text-gray-700 text-[10px] ml-1">({session.name})</span>
                    {/if}
                  </button>
                {/if}
                </div>
                <div class="{stacked ? 'flex items-center gap-2 min-w-0 overflow-hidden pl-7 lg:pl-6' : 'contents'}">
                {#if section.showProject && session.project_alias}
                  <span class="text-[9px] min-w-0 truncate text-gray-600 border border-gray-800 px-1 rounded-sm" title={session.project_alias}>{session.project_alias}</span>
                {/if}
                {#if section.showActivity}
                  {@const activity = relativeActivity(session, activityNow)}
                  <time datetime={session.activity_at || ''} title={activity.label} aria-label={activity.label} class="text-[9px] text-gray-700 shrink-0">{activity.display}</time>
                {/if}
                <span
                  class="hidden lg:inline text-[9px] shrink-0 uppercase tracking-wide px-1 py-px border border-gray-800 text-gray-600 rounded-sm"
                  title={`Target: ${targetLabel(session)}`}
                >{targetLabel(session)}</span>
                {#if session.artifact_count > 0}
                  <span class="text-cyan-500 text-[10px] shrink-0" title={`${session.artifact_count} artifact${session.artifact_count === 1 ? '' : 's'}`}>◆ {session.artifact_count}</span>
                {/if}
                {#each session.status?.badges || [] as badge}
                  <span class="text-[10px] shrink-0" style="color: {colorMap[session.status?.color] || colorMap.gray}" title={statusTitle(session)}>{badge}</span>
                {/each}
                </div>
              </div>

              <!-- Action buttons:
                   Mobile (< lg): always visible, generous touch targets
                   Desktop (≥ lg): invisible by default, fade in on group hover -->
              <div class="
                flex items-center gap-px pr-1
                lg:opacity-0 lg:group-hover:opacity-100 lg:group-focus-within:opacity-100 lg:transition-opacity
              ">
                <button
                  type="button"
                  data-pin-session={session.name}
                  on:click={() => handlePin(session, true)}
                  disabled={!!pendingPins[session.name]}
                  aria-pressed={!!session.pinned}
                  aria-label={`${session.pinned ? 'Unpin' : 'Pin'} ${session.display_name || session.name}`}
                  class="font-mono text-base lg:text-[11px] min-w-11 min-h-11 lg:min-w-7 lg:min-h-7 px-2 lg:px-1 focus-visible:outline focus-visible:outline-2 focus-visible:outline-cyan-500 {session.pinned ? 'text-cyan-300 lg:opacity-100' : 'text-gray-700 hover:text-cyan-400'}"
                  title={session.pinned ? 'unpin session' : 'pin session'}
                ><span aria-hidden="true">{pendingPins[session.name] ? '…' : session.pinned ? '●' : '○'}</span></button>
                {#if session.gatepost?.logs_url}
                  <a
                    href={session.gatepost.logs_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="
                      font-mono text-emerald-600 hover:text-emerald-300 active:text-emerald-200
                      text-sm lg:text-[10px]
                      px-3 lg:px-1.5 py-4 lg:py-1.5
                      transition-colors
                    "
                    title="Open Gatepost log viewer"
                  >logs</a>
                {/if}
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
                  disabled={!!deletingSessions[session.name]}
                  class="
                    font-mono
                    {deletingSessions[session.name]
                      ? 'text-cyan-400 cursor-wait'
                      : pendingDelete === session.name
                        ? 'text-red-400'
                        : 'text-red-700 hover:text-red-400 active:text-red-300'}
                    text-lg lg:text-[10px]
                    px-3 lg:px-1.5 py-4 lg:py-1.5
                    transition-colors
                  "
                  title={deletingSessions[session.name] ? 'removing session…' : pendingDelete === session.name ? 'click again to confirm' : 'delete'}
                  aria-label={deletingSessions[session.name] ? `removing ${session.name}` : pendingDelete === session.name ? `confirm delete ${session.name}` : `delete ${session.name}`}
                >{deletingSessions[session.name] ? '…' : pendingDelete === session.name ? '!×' : '×'}</button>
              </div>
            </div>

            <!-- Routes inline expansion -->
            {#if expandedRoutes === session.name}
              <div role="listitem" aria-label={`Services for ${session.display_name || session.name}`} class="bg-[#0d1117] border-b border-[#1e2d4a] pl-6 pr-3 py-2 space-y-1.5">
                {#each Object.entries(routes) as [svc, url]}
                  <a
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    on:click={(e) => openExternal(e, url)}
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

  <!-- Status legend -->
  {#if showStatusHelp}
    <div class="border-t border-[#1e2d4a] bg-[#080c16] px-3 py-3 text-[10px] font-mono text-gray-500 shrink-0 space-y-2">
      <div class="flex items-center justify-between text-gray-400">
        <span class="uppercase tracking-wider">status colors</span>
        <button on:click={() => showStatusHelp = false} class="text-gray-700 hover:text-gray-400">×</button>
      </div>
      <div class="grid grid-cols-1 gap-1.5">
        <div><span class="text-orange-500">● ! attention</span> — explicitly flagged</div>
        <div><span class="text-cyan-500">● new ◆</span> — unseen artifacts</div>
        <div><span class="text-red-500">● ⚠ repair</span> — missing/inaccessible worktree or unknown git state</div>
        <div><span class="text-yellow-500">● ± dirty</span> — uncommitted, untracked, or unpushed work</div>
        <div><span class="text-green-500">● ▶ active</span> — tmux/editor/recent activity</div>
        <div><span class="text-gray-500">● 🧹 cleanup</span> — full scan verified safe to prune</div>
        <div><span class="text-gray-500">● scan</span> — old/stopped in fast list, not git-verified yet</div>
      </div>
      <div class="pt-1 border-t border-[#1e2d4a] text-gray-600">
        Target chips show <span class="text-gray-400">host</span>, <span class="text-gray-400">docker</span>, or <span class="text-gray-400">gatepost</span>. Gatepost sessions with logs expose a <span class="text-emerald-500">logs</span> link.
      </div>
    </div>
  {/if}

  <!-- Key hint bar (desktop only) -->
  <div class="hidden lg:flex items-center gap-4 px-3 h-7 border-t border-[#1e2d4a] text-[10px] font-mono text-gray-700 shrink-0 select-none">
    <button on:click={() => showStatusHelp = !showStatusHelp} class="text-gray-600 hover:text-cyan-400 border border-[#1e2d4a] px-1 leading-none" title="status color legend">?</button>
    <span>↑↓ nav</span>
    <span>⏎ open</span>
    <span>/ search</span>
    <span>⇧P pin</span>
    <span class="ml-auto">^⇧C new</span>
    <span>^⇧S focus</span>
  </div>

</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={handleCreated} />
{/if}

<style>
  @keyframes flag-flash {
    0%   { background-color: transparent; }
    8%   { background-color: rgba(234, 179, 8, 0.22); } /* amber burst */
    100% { background-color: transparent; }
  }
  .flag-flash {
    animation: flag-flash 3s ease-out forwards;
  }
</style>
