<!-- web/app/src/lib/usage/UsageHeaderPill.svelte -->
<!--
  Compact provider-usage summary for the session header's open middle section
  (SessionTopbar), sitting between the session facts and the Status toggle.

  This is the same data as UsageStrip but tuned for a single 48px-tall row: one
  short group per provider (name · bar · percent) and no reset times, so it
  reads at a glance without competing with the facts strip. Clicking it opens
  the same detail modal via the shared devx:showUsage event.

  Desktop/tablet only: the session header itself is hidden below lg, where the
  actions menu carries the "Provider usage" entry instead.
-->
<script>
  import { tone, percent, toneTextClass, toneBarClass, stripAriaLabel } from './usageFormat.js'

  export let usage = null       // Usage shape from GET /api/usage, or null while unfetched

  $: state = usage?.state || 'loading'
  // Only providers with a usable primary window get a meter; a provider in the
  // error state would need more room than this row has, so it degrades to the
  // muted summary line below and the detail modal carries the message.
  $: providers = (usage?.providers || []).filter(p => p.state !== 'error' && p.primary)
  $: hasError = (usage?.providers || []).some(p => p.state === 'error')
  $: ariaLabel = stripAriaLabel(usage)

  function handleClick() {
    window.dispatchEvent(new CustomEvent('devx:showUsage'))
  }
</script>

{#if state === 'disabled' || state === 'loading'}
  <!-- Nothing while the first payload is in flight: a placeholder here would
       shift the facts strip on every page load. -->
{:else if state === 'unavailable' || providers.length === 0}
  <button
    type="button"
    on:click={handleClick}
    aria-label={ariaLabel}
    title="Redline is not reporting provider usage"
    class="shrink-0 px-2 h-full flex items-center text-[10px] font-mono text-gray-700 hover:text-gray-400 transition-colors"
  >
    usage&nbsp;·&nbsp;{hasError ? 'error' : 'n/a'}
  </button>
{:else}
  <button
    type="button"
    on:click={handleClick}
    aria-label={ariaLabel}
    title="Provider usage details"
    class="shrink-0 flex items-center gap-3 px-2 h-full text-[10px] font-mono text-gray-500 hover:text-gray-300 hover:bg-[#0d1117] transition-colors rounded-sm"
  >
    {#each providers as provider (provider.id)}
      {@const isStale = provider.state === 'stale'}
      {@const remaining = provider.primary?.remaining ?? 0}
      {@const t = tone(remaining)}
      {@const pct = percent(remaining)}
      <span class="flex items-center gap-1.5">
        <span class="text-gray-600">{provider.provider}</span>
        <span class="text-gray-700">{provider.primary?.label || 'wk'}</span>
        <span class="w-10 h-1.5 bg-gray-800 rounded-sm overflow-hidden">
          <span class="block h-full {isStale ? 'bg-gray-600' : toneBarClass(t)}" style="width: {pct}%"></span>
        </span>
        <span data-tone={isStale ? 'stale' : t} class="w-7 text-right {isStale ? 'text-gray-600' : toneTextClass(t)}">{pct}%</span>
      </span>
    {/each}
  </button>
{/if}
