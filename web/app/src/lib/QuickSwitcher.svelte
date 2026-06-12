<!-- web/app/src/lib/QuickSwitcher.svelte
     Cmd/Ctrl+P fuzzy session switcher. Highlighting a row prewarms its
     terminal (debounced) so the open is warm by the time the user hits Enter. -->
<script>
  import { onMount } from 'svelte'
  import { listSessions, prewarmTerminal } from '../api.js'
  import { markPrewarmed } from './stores/sessionUiState.js'

  export let onSelect   // (session) => void
  export let onClose    // () => void

  let query = ''
  let sessions = []
  let selectedIndex = 0
  let inputEl
  let listEl

  const PREWARM_DEBOUNCE_MS = 150
  let prewarmTimer = null
  let prewarmRequested = new Set()

  onMount(() => {
    inputEl?.focus()
    listSessions().then(s => { sessions = s }).catch(() => {})
    return () => clearTimeout(prewarmTimer)
  })

  // Subsequence fuzzy match against a haystack; returns a score (higher is
  // better) or -1 for no match. Favors prefix matches, word starts after
  // separators, and consecutive runs.
  function fuzzyScore(haystack, needle) {
    const h = haystack.toLowerCase()
    const n = needle.toLowerCase()
    if (!n) return 0
    let score = 0, hi = 0, prevMatch = -2
    for (let ni = 0; ni < n.length; ni++) {
      const idx = h.indexOf(n[ni], hi)
      if (idx === -1) return -1
      if (idx === 0) score += 8
      else if ('-_/. '.includes(h[idx - 1])) score += 6
      if (idx === prevMatch + 1) score += 4
      score -= (idx - hi) * 0.1  // small gap penalty
      prevMatch = idx
      hi = idx + 1
    }
    return score
  }

  function sessionScore(s, q) {
    const fields = [s.display_name, s.name, s.branch, s.project_alias].filter(Boolean)
    let best = -1
    for (const f of fields) {
      const sc = fuzzyScore(f, q)
      if (sc > best) best = sc
    }
    return best
  }

  $: filtered = query.trim()
    ? sessions
        .map(s => ({ s, score: sessionScore(s, query.trim()) }))
        .filter(x => x.score >= 0)
        .sort((a, b) => b.score - a.score || a.s.name.localeCompare(b.s.name))
        .map(x => x.s)
    : sessions

  // Clamp selection when the result set changes, then prewarm the highlight.
  $: {
    if (selectedIndex >= filtered.length) selectedIndex = Math.max(0, filtered.length - 1)
    schedulePrewarm(filtered[selectedIndex]?.name)
  }

  function schedulePrewarm(name) {
    clearTimeout(prewarmTimer)
    if (!name || prewarmRequested.has(name)) return
    prewarmTimer = setTimeout(() => {
      prewarmRequested.add(name)
      prewarmTerminal(name)
        .then(status => markPrewarmed(name, !!status?.ready))
        .catch(() => prewarmRequested.delete(name))
    }, PREWARM_DEBOUNCE_MS)
  }

  function scrollSelectedIntoView() {
    listEl?.children[selectedIndex]?.scrollIntoView({ block: 'nearest' })
  }

  function handleKeydown(e) {
    // stopPropagation on every handled key: SessionList has a window-level
    // keydown handler (Enter opens the highlighted sidebar row, arrows move
    // its cursor) that must not also react to keys aimed at the switcher.
    if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) {
      e.preventDefault()
      e.stopPropagation()
      selectedIndex = Math.min(selectedIndex + 1, filtered.length - 1)
      scrollSelectedIntoView()
    } else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) {
      e.preventDefault()
      e.stopPropagation()
      selectedIndex = Math.max(selectedIndex - 1, 0)
      scrollSelectedIntoView()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      if (filtered[selectedIndex]) onSelect(filtered[selectedIndex])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }
</script>

<div
  class="absolute inset-0 z-50 bg-black/60 flex items-start justify-center pt-[12vh] px-6"
  on:pointerdown|self={onClose}
>
  <div class="w-full max-w-lg bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-2xl overflow-hidden">
    <div class="flex items-center gap-2 px-3 py-2 border-b border-[#13213a]">
      <span class="text-cyan-500 text-xs font-mono shrink-0">⌕</span>
      <input
        bind:this={inputEl}
        bind:value={query}
        on:keydown={handleKeydown}
        placeholder="jump to session…"
        class="flex-1 bg-transparent outline-none text-sm font-mono text-gray-200 placeholder-gray-700"
        aria-label="session switcher search"
      />
      <span class="text-[10px] font-mono text-gray-700 shrink-0">↵ open · esc close</span>
    </div>
    <div bind:this={listEl} class="max-h-[50vh] overflow-y-auto">
      {#each filtered.slice(0, 50) as s, i (s.name)}
        <button
          type="button"
          class="
            w-full flex items-center gap-2 px-3 py-1.5 text-left font-mono text-xs
            {i === selectedIndex ? 'bg-cyan-950/40 text-cyan-200' : 'text-gray-400 hover:bg-[#0d1117]'}
          "
          on:pointerenter={() => { selectedIndex = i }}
          on:click={() => onSelect(s)}
        >
          <span class="truncate">{s.display_name || s.name}</span>
          {#if s.display_name && s.display_name !== s.name}
            <span class="text-gray-700 text-[10px] truncate">({s.name})</span>
          {/if}
          {#if s.attention_flag}
            <span class="text-yellow-500 text-[10px] shrink-0">◆</span>
          {/if}
          <span class="flex-1"></span>
          <span class="text-gray-700 text-[10px] shrink-0">{s.project_alias || ''}</span>
        </button>
      {:else}
        <div class="px-3 py-4 text-xs font-mono text-gray-700">no matching sessions</div>
      {/each}
    </div>
  </div>
</div>
