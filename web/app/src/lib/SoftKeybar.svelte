<!-- web/app/src/lib/SoftKeybar.svelte -->
<script>
  export let onKey

  // Keys are tmux key names passed to `tmux send-keys -t session KEY`.
  // Space-separated values send multiple keystrokes (e.g. 'C-b C-b').
  const row1 = [
    { label: 'Ctrl-B', key: 'C-b' },
    { label: 'Ctrl-C', key: 'C-c' },
    { label: 'Esc',    key: 'Escape' },
    { label: 'Tab',    key: 'Tab' },
    { label: 'Ctrl-Z', key: 'C-z' },
  ]
  const row2 = [
    { label: '↑', key: 'Up' },
    { label: '↓', key: 'Down' },
    { label: '←', key: 'Left' },
    { label: '→', key: 'Right' },
  ]
  const row3 = [
    { label: 'S-Tab',   key: 'BTab' },
    { label: 'Ctrl-O',  key: 'C-o' },
    { label: 'C-b C-b', key: 'C-b C-b' },
  ]
</script>

<div class="flex flex-col gap-1 px-2 py-2 bg-gray-900 border-t border-gray-800">
  <div class="flex gap-1">
    {#each row1 as k}
      <button
        on:click={() => onKey(k.key)}
        class="flex-1 bg-gray-700 hover:bg-gray-600 active:bg-gray-500 text-white text-xs font-mono py-2 rounded text-center transition-colors"
      >
        {k.label}
      </button>
    {/each}
  </div>
  <div class="flex gap-1">
    {#each row2 as k}
      <button
        on:click={() => onKey(k.key)}
        class="flex-1 bg-gray-700 hover:bg-gray-600 active:bg-gray-500 text-white text-xs font-mono py-2 rounded text-center transition-colors"
      >
        {k.label}
      </button>
    {/each}
  </div>
  <div class="flex gap-1">
    {#each row3 as k}
      <button
        on:click={() => onKey(k.key)}
        class="flex-1 bg-gray-700 hover:bg-gray-600 active:bg-gray-500 text-white text-xs font-mono py-2 rounded text-center transition-colors"
      >
        {k.label}
      </button>
    {/each}
  </div>
</div>
