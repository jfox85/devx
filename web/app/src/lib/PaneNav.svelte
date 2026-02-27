<!-- web/app/src/lib/PaneNav.svelte -->
<script>
  export let windows = []   // [{index: 1, name: 'zsh', active: true}]
  export let onSwitch       // (windowIndex) => void
</script>

{#if windows.length > 0}
  <div class="flex gap-1 px-2 py-1 bg-gray-800 border-b border-gray-700 overflow-x-auto">
    {#each windows as win}
      <button
        on:click={() => onSwitch(win.index)}
        class="text-xs py-1 px-3 rounded flex-shrink-0 transition-colors
               {win.active ? 'bg-blue-700 text-white' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
      >
        {win.index}: {win.name}
      </button>
    {/each}
  </div>
{/if}
