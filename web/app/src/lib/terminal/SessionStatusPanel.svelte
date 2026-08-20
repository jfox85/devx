<!-- web/app/src/lib/terminal/SessionStatusPanel.svelte -->
<!--
  Slide-over status drawer for the active session: derived status + reasons,
  worktree/tmux/editor facts, routes (external preferred), and activity times.
  Shows only fields the API actually returns; the session prop may be a stub.
-->
<script>
  import { relativeActivity } from '../sessionOrdering.js'
  import { openExternal as openExternalDesktop } from '../desktopBridge.js'

  export let session
  export let onClose = () => {}

  let panelEl

  const pillColor = {
    green: 'text-green-400', cyan: 'text-cyan-300', orange: 'text-orange-400',
    yellow: 'text-yellow-400', red: 'text-red-400', gray: 'text-gray-500',
  }

  function targetLabel(s) {
    if (s.gatepost?.enabled) return s.gatepost.bypass ? 'gatepost bypass' : 'gatepost'
    if (s.target_type === 'docker') return 'docker'
    if (s.target_type === 'gatepost') return 'gatepost'
    return 'host'
  }

  function allRoutes(s) {
    const result = {}
    for (const [svc, host] of Object.entries(s.external_routes || {})) {
      result[svc] = host.startsWith('http') ? host : 'https://' + host
    }
    for (const [svc, url] of Object.entries(s.routes || {})) {
      if (result[svc]) continue
      result[svc] = url.startsWith('http') ? url : 'https://' + url
    }
    return result
  }

  function openExternal(e, url) {
    if (openExternalDesktop(url, () => {})) e.preventDefault()
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onClose()
    }
  }

  $: status = session.status || null
  $: routes = allRoutes(session)
  $: routeEntries = Object.entries(routes)
  $: activity = relativeActivity(session)
  $: facts = [
    ['target', targetLabel(session)],
    ['tmux', status?.tmux_status || 'unknown'],
    ['worktree', status ? (status.worktree_exists ? (status.dirty ? 'dirty' : 'clean') : 'missing') : 'unknown'],
    ['editor', status ? (status.editor_running ? 'running' : 'stopped') : 'unknown'],
    ['artifacts', String(session.artifact_count ?? 0)],
    ['activity', activity.label.toLowerCase()],
  ]
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="fixed inset-0 z-[60] lg:bg-transparent bg-black/50" on:click={onClose}>
  <div
    bind:this={panelEl}
    role="dialog"
    tabindex="-1"
    class="absolute right-0 top-0 bottom-0 w-full max-w-sm bg-[#0b1020] border-l border-[#1e2d4a] shadow-2xl overflow-y-auto"
    aria-label="Session status details"
    on:click|stopPropagation
    on:keydown={handleKeydown}
  >
    <div class="flex items-center justify-between px-4 h-12 border-b border-[#1e2d4a] sticky top-0 bg-[#0b1020]">
      <span class="text-xs font-mono font-bold uppercase tracking-widest text-cyan-500">Status</span>
      <button on:click={onClose} aria-label="Close status panel" class="text-gray-500 hover:text-gray-200 font-mono px-2 min-h-11 lg:min-h-0">×</button>
    </div>

    <div class="px-4 py-3 border-b border-[#111a2e]">
      <div class="text-sm font-mono text-gray-200 truncate">{session.display_name || session.name}</div>
      {#if status?.label}
        <div class="text-xs font-mono mt-1 {pillColor[status.color] || 'text-gray-500'}">● {status.label}</div>
      {/if}
      {#each status?.reasons || [] as reason}
        <div class="text-[11px] font-mono text-gray-500 mt-1">{reason}</div>
      {/each}
    </div>

    <div class="px-4 py-3 border-b border-[#111a2e]">
      <div class="text-[10px] font-mono uppercase tracking-widest text-gray-600 mb-2">Facts</div>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
        {#each facts as [label, value] (label)}
          <dt class="text-[11px] font-mono text-gray-600">{label}</dt>
          <dd class="text-[11px] font-mono text-gray-300 truncate">{value}</dd>
        {/each}
      </dl>
    </div>

    <div class="px-4 py-3">
      <div class="text-[10px] font-mono uppercase tracking-widest text-gray-600 mb-2">Routes</div>
      {#if routeEntries.length === 0}
        <div class="text-[11px] font-mono text-gray-600">no routes</div>
      {:else}
        {#each routeEntries as [svc, url] (svc)}
          <a
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            on:click={(e) => openExternal(e, url)}
            class="flex items-center gap-2 min-h-11 lg:min-h-9 text-[11px] font-mono text-cyan-500 hover:text-cyan-300 truncate"
          >
            <span class="text-gray-500 w-14 shrink-0">{svc}</span>
            <span class="truncate">{url.replace(/^https?:\/\//, '')}</span>
            <span aria-hidden="true" class="text-gray-700">↗</span>
          </a>
        {/each}
      {/if}
    </div>
  </div>
</div>
