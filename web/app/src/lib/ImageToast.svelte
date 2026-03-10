<!-- web/app/src/lib/ImageToast.svelte -->
<script>
  import { onDestroy } from 'svelte'

  export let upload = null  // { path, objectURL } | null
  export let error = null   // string | null
  export let onDismiss

  let timer

  // Last two path segments for display
  $: shortPath = upload?.path
    ? upload.path.split('/').slice(-2).join('/')
    : ''

  // Restart auto-dismiss timer whenever upload changes (handles back-to-back
  // uploads where the component stays mounted and onMount doesn't re-fire).
  $: {
    clearTimeout(timer)
    if (upload) {
      timer = setTimeout(onDismiss, 3000)
    }
  }

  onDestroy(() => {
    clearTimeout(timer)
  })
</script>

<div class="absolute bottom-12 right-3 z-50 max-w-xs font-mono text-xs shadow-lg">
  {#if upload}
    <div class="flex items-center gap-2 bg-[#0d1117] border border-cyan-800 px-3 py-2">
      <img src={upload.objectURL} alt="" class="w-10 h-10 object-cover rounded shrink-0" />
      <div class="flex-1 min-w-0">
        <div class="text-cyan-300 truncate">{shortPath}</div>
        <div class="text-green-500 text-[10px]">inserted</div>
      </div>
      <button
        on:click={onDismiss}
        class="text-gray-600 hover:text-gray-300 ml-1 shrink-0"
        aria-label="dismiss"
      >×</button>
    </div>
  {:else if error}
    <div class="flex items-center gap-2 bg-[#0d1117] border border-red-800 px-3 py-2">
      <div class="flex-1 min-w-0">
        <div class="text-red-400">{error}</div>
      </div>
      <button
        on:click={onDismiss}
        class="text-gray-600 hover:text-gray-300 ml-1 shrink-0"
        aria-label="dismiss"
      >×</button>
    </div>
  {/if}
</div>
