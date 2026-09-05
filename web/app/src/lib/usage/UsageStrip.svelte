<!-- web/app/src/lib/usage/UsageStrip.svelte -->
<script>
  import { tone, percent, relativeReset, toneTextClass, toneBarClass, staleLabel, stripAriaLabel } from './usageFormat.js'

  export let usage = null       // Usage shape from GET /api/usage, or null while unfetched

  $: state = usage?.state || 'loading'
  $: providers = usage?.providers || []
  $: ariaLabel = stripAriaLabel(usage)

  function handleClick() {
    window.dispatchEvent(new CustomEvent('devx:showUsage'))
  }
</script>

{#if state === 'disabled'}
  <!-- v1: nothing mounted when usage is turned off. -->
{:else if state === 'loading'}
  <div class="hidden lg:flex items-center px-3 h-7 border-t border-[#1e2d4a] text-[10px] font-mono text-gray-700 shrink-0 select-none">
    usage · …
  </div>
{:else if state === 'unavailable'}
  <!-- Desktop only — mobile hides this to save vertical space (plan: UX States). -->
  <div class="hidden lg:flex items-center px-3 h-7 border-t border-[#1e2d4a] text-[10px] font-mono text-gray-600 shrink-0 select-none">
    usage · redline not running
  </div>
{:else}
  <button
    type="button"
    on:click={handleClick}
    aria-label={ariaLabel}
    class="w-full flex flex-col border-t border-[#1e2d4a] bg-[#0a0e1a] hover:bg-[#0d1117] transition-colors text-left shrink-0"
  >
    {#each providers as provider, i (provider.id)}
      {@const isError = provider.state === 'error'}
      {@const isStale = provider.state === 'stale'}
      {@const primary = provider.primary}
      {@const remaining = primary?.remaining ?? 0}
      {@const t = tone(remaining)}
      {@const pct = percent(remaining)}
      {@const rel = primary ? relativeReset(primary.resets_at) : '—'}
      {@const stale = isStale ? staleLabel(provider.observed_at) : ''}
      <div class="flex items-center gap-2 px-3 min-h-11 lg:min-h-0 lg:h-6 text-[11px] lg:text-[10px] font-mono">
        <span class="text-gray-700 w-9 shrink-0">{i === 0 ? 'usage' : ''}</span>
        {#if isError}
          <span class="text-red-400 truncate">{provider.provider}: {provider.error || 'error'}</span>
        {:else}
          <span class="text-gray-500 w-12 shrink-0 truncate">{provider.provider}</span>
          <span class="text-gray-600 w-6 shrink-0 truncate">{primary?.label || 'wk'}</span>
          <span class="flex-1 h-1.5 bg-gray-800 rounded-sm overflow-hidden min-w-8 max-w-24">
            <span class="block h-full {isStale ? 'bg-gray-600' : toneBarClass(t)}" style="width: {pct}%"></span>
          </span>
          <span data-tone={isStale ? 'stale' : t} class="w-8 shrink-0 text-right {isStale ? 'text-gray-600' : toneTextClass(t)}">{pct}%</span>
          <span class="text-gray-700 shrink-0">· {rel}</span>
          {#if stale}
            <span class="text-gray-600 shrink-0">· {stale}</span>
          {/if}
        {/if}
        {#if i === 0}
          <span class="ml-auto text-gray-700 shrink-0" aria-hidden="true">▸</span>
        {/if}
      </div>
    {/each}
  </button>
{/if}
