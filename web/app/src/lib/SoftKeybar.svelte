<!-- web/app/src/lib/SoftKeybar.svelte -->
<script>
  export let onKey

  const keys = [
    { label: 'Ctrl-C', seq: '\x03' },
    { label: 'Esc',    seq: '\x1b' },
    { label: 'Tab',    seq: '\x09' },
    { label: '↑',      seq: '\x1b[A' },
    { label: '↓',      seq: '\x1b[B' },
    { label: '←',      seq: '\x1b[D' },
    { label: '→',      seq: '\x1b[C' },
    { label: 'Ctrl-Z', seq: '\x1a' },
  ]
</script>

<div class="flex gap-1 px-2 py-1 bg-gray-900 border-t border-gray-800 overflow-x-auto">
  {#each keys as key}
    <button
      on:click={() => onKey(key.seq)}
      class="min-w-[3rem] bg-gray-700 hover:bg-gray-600 active:bg-gray-500 text-white text-xs font-mono py-2 px-3 rounded flex-shrink-0 transition-colors"
    >
      {key.label}
    </button>
  {/each}
</div>
