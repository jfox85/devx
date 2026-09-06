<!-- web/app/src/lib/usage/UsageDetailModal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { refreshUsage } from '../../api.js'
  import { isDesktop, openExternal as openExternalDesktop } from '../desktopBridge.js'
  import { percent, relativeReset, absoluteReset, sampleAge, sampleAgePhrase, tone, toneTextClass, toneBarClass } from './usageFormat.js'

  export let usage = null      // Usage shape from GET /api/usage / SSE
  export let onClose = () => {}

  const REDLINE_DASHBOARD_URL = 'http://127.0.0.1:7436/'
  const REFRESH_TIMEOUT_MS = 15000

  let modalEl
  let closeButtonEl
  let refreshing = false
  let refreshTimer = null
  let refreshError = ''

  $: state = usage?.state || 'loading'
  $: providers = usage?.providers || []

  // Stop the spinner as soon as a new usage snapshot arrives (SSE push or the
  // refresh response itself already updated the App-owned `usage` prop).
  let lastUpdatedAt = usage?.updated_at
  $: if (refreshing && usage?.updated_at !== lastUpdatedAt) {
    lastUpdatedAt = usage?.updated_at
    stopRefreshSpinner()
  }

  function stopRefreshSpinner() {
    refreshing = false
    clearTimeout(refreshTimer)
    refreshTimer = null
  }

  async function handleRefresh() {
    if (refreshing) return
    refreshing = true
    refreshError = ''
    lastUpdatedAt = usage?.updated_at
    // Don't let a hung/throttled request leave the button spinning forever.
    refreshTimer = setTimeout(stopRefreshSpinner, REFRESH_TIMEOUT_MS)
    try {
      const { usage: fresh, status } = await refreshUsage()
      if (status !== 202) {
        // 202 (throttled) keeps waiting for the SSE push / timeout above; any
        // other outcome (200/404/502) is final immediately.
        stopRefreshSpinner()
      }
      lastUpdatedAt = fresh?.updated_at
    } catch (e) {
      refreshError = e.message || 'Refresh failed'
      stopRefreshSpinner()
    }
  }

  onMount(() => {
    // Focus something inside the dialog so Escape/Tab reach handleModalKeydown
    // (attached to modalEl, which only sees events bubbling from its own
    // descendants) instead of being swallowed by whatever had focus before
    // the modal opened (e.g. the header pill behind it). modalEl is the
    // fallback: it is tabindex="-1" and carries the handler itself.
    ;(closeButtonEl || modalEl)?.focus()
  })

  onDestroy(() => clearTimeout(refreshTimer))

  function focusableControls() {
    return Array.from(modalEl?.querySelectorAll('button, a, [tabindex]:not([tabindex="-1"])') || [])
      .filter(el => !el.disabled && el.offsetParent !== null)
  }

  function handleModalKeydown(e) {
    if (e.key === 'Escape') {
      onClose()
      return
    }
    if (e.key !== 'Tab') return
    const controls = focusableControls()
    if (controls.length === 0) return
    const first = controls[0]
    const last = controls[controls.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  function openRedlineDashboard(e) {
    if (openExternalDesktop(REDLINE_DASHBOARD_URL)) {
      e.preventDefault()
    }
  }
</script>

<!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
<div
  bind:this={modalEl}
  class="fixed inset-0 bg-black/70 flex items-end sm:items-center justify-center z-50 p-4"
  role="dialog" aria-modal="true" aria-label="Provider usage details" tabindex="-1"
  on:click|self={onClose}
  on:keydown={handleModalKeydown}
>
  <div class="w-full max-w-sm max-h-[90dvh] overflow-y-auto bg-[#0d1117] border border-[#1e2d4a]">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-2 border-b border-[#1e2d4a]">
      <span class="text-cyan-400 text-xs font-mono font-bold tracking-widest">provider usage</span>
      <div class="flex items-center gap-1">
        <button
          type="button"
          on:click={handleRefresh}
          disabled={refreshing || state === 'unavailable' || state === 'disabled'}
          aria-label="Refresh provider usage"
          title="refresh"
          class="text-gray-500 hover:text-cyan-400 disabled:opacity-40 font-mono text-xs px-1.5 py-0.5"
        >{refreshing ? '⟳' : '↻'}</button>
        <button
          bind:this={closeButtonEl}
          type="button"
          on:click={onClose}
          aria-label="Close"
          class="text-gray-600 hover:text-gray-400 font-mono text-xs px-1.5 py-0.5"
        >×</button>
      </div>
    </div>

    <div class="p-4 space-y-4">
      {#if refreshError}
        <p class="text-red-500 text-[11px] font-mono">{refreshError}</p>
      {/if}

      {#if state === 'disabled'}
        <p class="text-gray-500 text-xs font-mono">{usage?.message || 'Provider usage is disabled.'}</p>

      {:else if state === 'unavailable'}
        <p class="text-gray-400 text-xs font-mono">{usage?.message || 'Redline is not reachable.'}</p>
        <p class="text-gray-600 text-[11px] font-mono">
          Start Redline (<code class="text-gray-400">redline serve</code>) to see provider usage.
        </p>
        {#if providers.length > 0}
          <p class="text-gray-700 text-[10px] font-mono">Showing the last known values below.</p>
        {/if}
      {/if}

      {#if state === 'loading' && providers.length === 0}
        <div class="space-y-3" aria-hidden="true">
          {#each [0, 1] as skeleton}
            <div class="space-y-1.5 animate-pulse">
              <div class="h-3 w-24 bg-gray-800 rounded-sm"></div>
              <div class="h-2 w-full bg-gray-900 rounded-sm"></div>
              <div class="h-2 w-full bg-gray-900 rounded-sm"></div>
            </div>
          {/each}
        </div>
      {/if}

      {#each providers as provider (provider.id)}
        {@const isStale = provider.state === 'stale'}
        {@const isError = provider.state === 'error'}
        <section aria-label={`${provider.label || provider.provider} usage`} class="space-y-2">
          <div class="flex items-baseline justify-between">
            <span class="text-gray-300 text-[11px] font-mono font-bold uppercase tracking-wide">{provider.label || provider.provider}</span>
            {#if !isError}
              <span class="text-gray-600 text-[10px] font-mono">{provider.source || 'redline'} · sampled {sampleAgePhrase(provider.observed_at)}</span>
            {/if}
          </div>

          {#if isError}
            <p class="text-red-400 text-[11px] font-mono">{provider.error || 'No usage data available.'}</p>
          {:else}
            {#if isStale}
              <p class="text-gray-600 text-[10px] font-mono">Last known values (stale {sampleAge(provider.observed_at)})</p>
            {/if}
            {#each provider.windows || [] as w (w.key)}
              {@const t = tone(w.remaining)}
              {@const pct = percent(w.remaining)}
              <div class="space-y-1">
                <div class="flex items-center justify-between text-[11px] font-mono">
                  <span class="text-gray-400 truncate">{w.label}</span>
                  <span class={isStale ? 'text-gray-600' : toneTextClass(t)}>{pct}% left</span>
                </div>
                <div class="h-1.5 bg-gray-800 rounded-sm overflow-hidden">
                  <div class="h-full {isStale ? 'bg-gray-600' : toneBarClass(t)}" style="width: {pct}%"></div>
                </div>
                <div class="text-gray-700 text-[10px] font-mono">
                  resets {absoluteReset(w.resets_at)} ({relativeReset(w.resets_at)}){w.reset_inferred ? ' · reset inferred' : ''}
                </div>
              </div>
            {/each}
          {/if}
        </section>
      {/each}
    </div>

    {#if state !== 'disabled'}
      <div class="px-4 py-2 border-t border-[#1e2d4a] space-y-1">
        <p class="text-gray-700 text-[10px] font-mono">
          via redline · polled {sampleAgePhrase(usage?.updated_at)}
        </p>
        <a
          href={REDLINE_DASHBOARD_URL}
          target="_blank"
          rel="noopener noreferrer"
          on:click={openRedlineDashboard}
          class="hidden sm:inline-block text-cyan-600 hover:text-cyan-300 text-[10px] font-mono"
        >[open redline dashboard]</a>
      </div>
    {/if}
  </div>
</div>
