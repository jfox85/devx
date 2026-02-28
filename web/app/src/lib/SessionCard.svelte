<!-- web/app/src/lib/SessionCard.svelte -->
<script>
  export let session
  export let onOpen
  export let onDelete

  let showServices = false

  $: allRoutes = (() => {
    const result = {}
    for (const [svc, url] of Object.entries(session.routes || {})) {
      result[svc] = url.startsWith('http') ? url : 'https://' + url
    }
    for (const [svc, host] of Object.entries(session.external_routes || {})) {
      result[svc] = 'https://' + host
    }
    return result
  })()

  $: hasRoutes = Object.keys(allRoutes).length > 0
</script>

<div class="bg-gray-900 rounded-xl overflow-hidden {session.attention_flag ? 'ring-1 ring-yellow-500' : ''}">
  <!-- Main tap target = open terminal -->
  <button on:click={() => onOpen(session)}
    class="w-full px-4 py-5 text-left hover:bg-gray-800 active:bg-gray-700 transition-colors">
    <div class="flex items-center gap-2">
      <span class="text-gray-500 text-lg leading-none flex-shrink-0">⌨</span>
      <span class="text-white font-semibold text-base leading-tight flex-1">{session.name}</span>
      {#if session.attention_flag}
        <span class="w-2 h-2 rounded-full bg-yellow-400 flex-shrink-0"></span>
      {/if}
      <span class="text-gray-500 text-sm flex-shrink-0">›</span>
    </div>
  </button>

  <!-- Action strip -->
  <div class="flex border-t border-gray-800">
    {#if hasRoutes}
      <button on:click={() => showServices = true}
        class="flex-1 text-blue-400 text-sm py-2 px-3 hover:bg-gray-800 active:bg-gray-700 transition-colors text-center">
        Services
      </button>
    {/if}
    <button on:click={() => onDelete(session)}
      class="text-red-500 text-sm py-2 px-4 hover:bg-gray-800 active:bg-gray-700 transition-colors
             {hasRoutes ? 'border-l border-gray-800' : 'flex-1 text-center'}">
      Delete
    </button>
  </div>
</div>

<!-- Services bottom sheet -->
{#if showServices}
  <!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
  <div class="fixed inset-0 bg-black/60 flex items-end justify-center z-50"
       role="dialog" aria-modal="true" tabindex="-1"
       on:click|self={() => showServices = false}
       on:keydown={(e) => e.key === 'Escape' && (showServices = false)}>
    <div class="w-full max-w-md bg-gray-900 rounded-t-2xl p-5 pb-10">
      <div class="w-10 h-1 bg-gray-700 rounded-full mx-auto mb-4"></div>
      <h2 class="text-white font-semibold text-base mb-4">{session.name}</h2>
      <div class="flex flex-col gap-2">
        {#each Object.entries(allRoutes) as [name, url]}
          <a href={url} target="_blank" rel="noopener noreferrer"
             class="flex items-center justify-between bg-gray-800 hover:bg-gray-700 active:bg-gray-600 rounded-xl px-4 py-3 transition-colors">
            <span class="text-white font-medium">{name}</span>
            <span class="text-gray-400 text-xs ml-3 truncate max-w-[180px]">{url.replace('https://', '')}</span>
          </a>
        {/each}
      </div>
      <button on:click={() => showServices = false}
        class="mt-4 w-full bg-gray-800 hover:bg-gray-700 text-gray-300 py-3 rounded-xl text-sm transition-colors">
        Close
      </button>
    </div>
  </div>
{/if}
