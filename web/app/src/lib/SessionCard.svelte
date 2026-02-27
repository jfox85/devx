<!-- web/app/src/lib/SessionCard.svelte -->
<script>
  export let session
  export let onOpen
  export let onDelete
  export let onFlag
</script>

<div class="bg-gray-900 rounded-xl p-4 shadow {session.attention_flag ? 'border border-yellow-500' : ''}">
  <div class="flex items-start justify-between mb-2">
    <div>
      <h2 class="text-white font-semibold text-lg leading-tight">{session.name}</h2>
      <p class="text-gray-400 text-sm">{session.branch}</p>
      {#if session.project_alias}
        <span class="text-xs bg-gray-700 text-gray-300 rounded px-2 py-0.5 mt-1 inline-block">{session.project_alias}</span>
      {/if}
    </div>
    {#if session.attention_flag}
      <span class="text-yellow-400 text-xl" aria-label="Attention flag">!</span>
    {/if}
  </div>

  <!-- Service links -->
  {#if session.external_routes && Object.keys(session.external_routes).length > 0}
    <div class="flex flex-wrap gap-2 mb-3">
      {#each Object.entries(session.external_routes) as [svc, host]}
        <a href="https://{host}" target="_blank" rel="noopener noreferrer"
           class="text-xs bg-blue-900 text-blue-200 rounded-full px-3 py-1 hover:bg-blue-700 transition-colors">
          {svc}
        </a>
      {/each}
    </div>
  {/if}

  <!-- Actions -->
  <div class="flex gap-2 mt-3">
    <button on:click={() => onOpen(session)}
      class="flex-1 bg-green-700 hover:bg-green-600 text-white text-sm font-medium py-2 rounded-lg transition-colors">
      Terminal
    </button>
    <button on:click={() => onFlag(session)}
      aria-label="Toggle attention flag"
      class="bg-gray-700 hover:bg-gray-600 text-white text-sm py-2 px-3 rounded-lg transition-colors">
      !
    </button>
    <button on:click={() => onDelete(session)}
      aria-label="Delete session"
      class="bg-red-900 hover:bg-red-700 text-white text-sm py-2 px-3 rounded-lg transition-colors">
      x
    </button>
  </div>
</div>
