<script>
  export let mode = 'insert'
  export let query = ''
  export let items = []
  export let selectedIndex = 0
  export let onQuery = () => {}
  export let onClose = () => {}
  export let onMove = () => {}
  export let onChoose = () => {}
  export let onOpen = () => {}
  export let onInsert = () => {}
</script>

<div class="absolute inset-0 z-50 bg-black/70 flex items-start justify-center p-8" on:click={onClose}>
  <div class="w-full max-w-2xl bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-xl overflow-hidden" on:click|stopPropagation>
    <div class="flex items-center border-b border-[#1e2d4a] px-3 py-2 gap-2">
      <div class="text-[11px] font-mono text-cyan-300 shrink-0">{mode === 'open' ? 'open' : 'insert'}</div>
      <input
        class="flex-1 bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500"
        value={query}
        placeholder={mode === 'open' ? 'Search artifacts to open...' : 'Search artifacts to insert path...'}
        autofocus
        on:input={(e) => onQuery(e.target.value)}
        on:keydown={(e) => {
          if (e.key === 'Escape') onClose()
          if (e.key === 'ArrowDown' || e.key === 'j') { e.preventDefault(); onMove(1) }
          if (e.key === 'ArrowUp' || e.key === 'k') { e.preventDefault(); onMove(-1) }
          if (e.key === 'Enter' && items[selectedIndex]) onChoose(items[selectedIndex])
        }}
      />
      <button class="text-gray-500 hover:text-cyan-300 text-xs font-mono" on:click={onClose}>[×]</button>
    </div>
    <div class="max-h-96 overflow-auto">
      {#if items.length === 0}
        <div class="p-4 text-xs font-mono text-gray-500">no matching artifacts</div>
      {:else}
        {#each items as a, i}
          <div class="flex items-center gap-2 border-b border-[#111a2e] hover:bg-cyan-950/30 {i === selectedIndex ? 'bg-cyan-950/30' : ''}">
            <button class="flex-1 min-w-0 text-left px-4 py-3" on:click={() => onChoose(a)}>
              <div class="text-sm text-gray-100 truncate">{a.title}</div>
              <div class="text-[11px] font-mono text-gray-500 truncate">{a.type} · {a.path}</div>
            </button>
            <button class="px-3 py-2 mr-1 text-[11px] font-mono text-cyan-300 hover:text-cyan-100 border border-cyan-950 rounded" title="open in artifact viewer" on:click={() => onOpen(a)}>[open]</button>
            <button class="px-3 py-2 mr-3 text-[11px] font-mono text-gray-500 hover:text-cyan-300 border border-[#1e2d4a] rounded" title="insert path into terminal" on:click={() => onInsert(a.path)}>[insert]</button>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>
