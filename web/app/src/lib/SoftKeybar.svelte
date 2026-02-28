<!-- web/app/src/lib/SoftKeybar.svelte -->
<script>
  export let onKey
  export let disabled = false

  const row1 = [
    { label: 'Ctrl-B', seq: '\x02' },
    { label: 'Ctrl-C', seq: '\x03' },
    { label: 'Esc',    seq: '\x1b' },
    { label: 'Tab',    seq: '\x09' },
    { label: 'Ctrl-Z', seq: '\x1a' },
  ]
  const row2 = [
    { label: '↑', seq: '\x1b[A' },
    { label: '↓', seq: '\x1b[B' },
    { label: '←', seq: '\x1b[D' },
    { label: '→', seq: '\x1b[C' },
  ]
</script>

<div class="flex flex-col gap-1 px-2 py-2 bg-gray-900 border-t border-gray-800">
  <div class="flex gap-1">
    {#each row1 as key}
      <button
        on:click={() => onKey(key.seq)}
        {disabled}
        class="flex-1 bg-gray-700 text-white text-xs font-mono py-2 rounded text-center transition-colors
               {disabled ? 'opacity-40 cursor-not-allowed' : 'hover:bg-gray-600 active:bg-gray-500'}"
      >
        {key.label}
      </button>
    {/each}
  </div>
  <div class="flex gap-1">
    {#each row2 as key}
      <button
        on:click={() => onKey(key.seq)}
        {disabled}
        class="flex-1 bg-gray-700 text-white text-xs font-mono py-2 rounded text-center transition-colors
               {disabled ? 'opacity-40 cursor-not-allowed' : 'hover:bg-gray-600 active:bg-gray-500'}"
      >
        {key.label}
      </button>
    {/each}
  </div>
</div>
