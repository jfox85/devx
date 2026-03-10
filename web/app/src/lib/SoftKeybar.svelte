<!-- web/app/src/lib/SoftKeybar.svelte -->
<script>
  export let onKey

  // Keys are tmux key names passed to `tmux send-keys -t session KEY`.
  // Space-separated values send multiple keystrokes (e.g. 'C-b C-b').
  const row1 = [
    { label: 'C-b',   key: 'C-b' },
    { label: 'C-c',   key: 'C-c' },
    { label: 'Esc',   key: 'Escape' },
    { label: 'Tab',   key: 'Tab' },
    { label: 'C-z',   key: 'C-z' },
  ]
  const row2 = [
    { label: '↑', key: 'Up' },
    { label: '↓', key: 'Down' },
    { label: '←', key: 'Left' },
    { label: '→', key: 'Right' },
  ]
  const row3 = [
    { label: 'S-Tab',   key: 'BTab' },
    { label: 'C-o',     key: 'C-o' },
    { label: 'C-b C-b', key: 'C-b C-b' },
  ]
</script>

<div class="flex flex-col gap-px bg-[#1e2d4a] border-t border-[#1e2d4a]">
  {#each [row1, row2, row3] as row}
    <div class="flex gap-px">
      {#each row as k}
        <button
          on:click={() => onKey(k.key)}
          class="
            flex-1 bg-[#0a0e1a] hover:bg-[#0d1117] active:bg-cyan-950/30
            text-gray-600 hover:text-gray-300 active:text-cyan-400
            text-[10px] font-mono py-2.5 text-center
            transition-colors
          "
        >{k.label}</button>
      {/each}
    </div>
  {/each}
</div>
