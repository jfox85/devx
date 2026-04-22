<!-- FlagToast.svelte — in-app toast shown when the browser window is focused -->
<script>
  import { onDestroy } from 'svelte'

  export let flagEvent = null   // { session, reason }
  export let onDismiss = () => {}
  export let onNavigate = () => {}

  let timer

  // Restart the timer each time a new flag event arrives.
  $: if (flagEvent) {
    clearTimeout(timer)
    timer = setTimeout(() => onDismiss(), 12000)
  }

  onDestroy(() => clearTimeout(timer))

  function go() {
    onNavigate(flagEvent.session)
    onDismiss()
  }
</script>

{#if flagEvent}
  <div
    role="alert"
    class="
      fixed bottom-4 right-4 z-50
      flex flex-col gap-2
      bg-[#0d1a2a] border border-cyan-700/70
      w-72 px-4 py-4 shadow-xl
      font-mono
    "
  >
    <div class="flex items-center gap-2">
      <span class="text-yellow-400">◆</span>
      <span class="text-cyan-300 font-bold text-sm truncate">{flagEvent.session}</span>
      <button
        on:click={onDismiss}
        class="ml-auto text-gray-600 hover:text-gray-400 transition-colors leading-none text-base"
        aria-label="dismiss"
      >×</button>
    </div>
    <div class="text-gray-400 text-xs pl-5">{flagEvent.reason}</div>
    <button
      on:click={go}
      class="mt-1 ml-5 self-start text-cyan-400 hover:text-cyan-200 text-sm transition-colors"
    >Go →</button>
  </div>
{/if}
