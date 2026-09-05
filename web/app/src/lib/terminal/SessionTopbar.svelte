<!-- web/app/src/lib/terminal/SessionTopbar.svelte -->
<!--
  Operator-console session header: project/branch crumbs + session title +
  status pill on the left, compact session facts inline (desktop only), and a
  Status toggle on the right. The session prop can be a minimal stub ({name})
  when opened from a notification, so every field access is guarded.
-->
<script>
  export let session
  export let statusOpen = false
  export let onToggleStatus = () => {}

  const pillColor = {
    green: 'text-green-400',
    cyan: 'text-cyan-300',
    orange: 'text-orange-400',
    yellow: 'text-yellow-400',
    red: 'text-red-400',
    gray: 'text-gray-500',
  }

  function targetLabel(s) {
    if (s.gatepost?.enabled) return s.gatepost.bypass ? 'gatepost bypass' : 'gatepost'
    if (s.target_type === 'docker') return 'docker'
    if (s.target_type === 'gatepost') return 'gatepost'
    return 'host'
  }

  $: title = session.display_name || session.name
  $: status = session.status || null
  $: routeCount = new Set([
    ...Object.keys(session.external_routes || {}),
    ...Object.keys(session.routes || {}),
  ]).size
  $: facts = status ? [
    { key: 'tmux', label: `tmux ${status.tmux_status || 'unknown'}`, tone: status.tmux_status === 'attached' || status.tmux_status === 'detached' ? 'text-gray-400' : 'text-gray-600' },
    { key: 'target', label: `${targetLabel(session)} target`, tone: 'text-gray-400' },
    ...(status.worktree_exists ? [{ key: 'worktree', label: status.dirty ? 'dirty worktree' : 'clean worktree', tone: status.dirty ? 'text-yellow-400' : 'text-gray-400' }] : []),
    ...(routeCount > 0 ? [{ key: 'routes', label: `${routeCount} route${routeCount === 1 ? '' : 's'}`, tone: 'text-gray-400' }] : []),
    { key: 'artifacts', label: `${session.artifact_count ?? 0} artifact${session.artifact_count === 1 ? '' : 's'}`, tone: 'text-gray-400' },
  ] : []
</script>

<!-- Desktop-only header: on phones this chrome would cost terminal rows, so the
     compact window bar (back + tabs + actions menu) stays the only mobile chrome
     and status details open from the actions menu instead. -->
<div class="hidden lg:flex items-center bg-[#0a0e1a] border-b border-[#1e2d4a] shrink-0 h-12 min-w-0">
  <div class="min-w-0 shrink-0 px-3 py-1 max-w-[38%]">
    {#if session.project_alias || session.branch}
      <div class="text-[10px] font-mono text-gray-600 truncate leading-tight">
        {#if session.project_alias}{session.project_alias}{/if}{#if session.project_alias && session.branch}&nbsp;/&nbsp;{/if}{#if session.branch}{session.branch}{/if}
      </div>
    {/if}
    <div class="flex items-center gap-2 min-w-0">
      <span class="text-sm font-mono font-bold text-gray-100 truncate" title={title}>{title}</span>
      {#if status?.label}
        <span class="text-[10px] font-mono shrink-0 {pillColor[status.color] || 'text-gray-500'}" title={[status.label, ...(status.reasons || [])].filter(Boolean).join(': ')}>
          ● {status.label}
        </span>
      {/if}
    </div>
  </div>

  <!-- Facts strip: compact real session facts inline in the header. -->
  <div class="flex items-center gap-0 min-w-0 flex-1 overflow-x-auto whitespace-nowrap px-2" aria-label="Session facts">
    {#each facts as fact, i (fact.key)}
      {#if i > 0}<span class="text-gray-800 px-2 select-none" aria-hidden="true">|</span>{/if}
      <span class="text-[11px] font-mono {fact.tone} shrink-0">{fact.label}</span>
    {/each}
  </div>

  <button
    on:click={onToggleStatus}
    aria-expanded={statusOpen}
    class="h-full px-3 text-xs font-mono shrink-0 border-l border-[#1e2d4a] flex items-center gap-1.5 transition-colors
      {statusOpen ? 'text-cyan-300 bg-cyan-950/30' : 'text-gray-500 hover:text-cyan-300'}"
    title="session status details and routes"
  >
    <span aria-hidden="true">〰</span><span>Status</span>
  </button>
</div>
