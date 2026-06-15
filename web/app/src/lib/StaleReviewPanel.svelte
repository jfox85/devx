<script>
  import { reviewSession } from '../api.js'

  export let staleReviewLoading = false
  export let staleCleanStatuses = []
  export let staleNeedsReviewStatuses = []
  export let staleDisplayStatuses = []
  export let sessionByName = {}
  export let pendingPruneStale = false
  export let pruningStale = false
  export let pendingDelete = null
  export let deletingSessions = {}
  export let onPrune = () => {}
  export let onOpen = () => {}
  export let onReviewed = () => {}
  export let onDelete = () => {}

  let expandedRows = {}
  let reviewByName = {}
  let reviewingByName = {}
  let reviewErrorByName = {}

  function reviewFor(status) {
    return reviewByName[status.session_name] || status.cleanup_review
  }

  async function runCleanupReview(status) {
    const name = status.session_name
    reviewingByName = { ...reviewingByName, [name]: true }
    reviewErrorByName = { ...reviewErrorByName, [name]: '' }
    try {
      const review = await reviewSession(name)
      reviewByName = { ...reviewByName, [name]: review }
    } catch (e) {
      reviewErrorByName = { ...reviewErrorByName, [name]: e.message || 'review failed' }
    } finally {
      reviewingByName = { ...reviewingByName, [name]: false }
    }
  }

  async function runVisibleReviews() {
    for (const status of staleDisplayStatuses) {
      if (!reviewFor(status) && !reviewingByName[status.session_name]) {
        await runCleanupReview(status)
      }
    }
  }

  function staleAge(status) {
    const seconds = status?.age_seconds || 0
    const days = Math.floor(seconds / 86400)
    if (days > 0) return `${days}d`
    return `${Math.floor(seconds / 3600)}h`
  }

  function staleReason(status) {
    return (status?.reasons || [status?.category || 'stale']).join('; ')
  }

  function staleProject(status) {
    const session = sessionByName[status.session_name]
    return session?.project_alias || '—'
  }

  function staleDisplayName(status) {
    const session = sessionByName[status.session_name]
    return session?.display_name || status.session_name
  }

  function reviewLabel(status) {
    const review = reviewFor(status)
    if (!review) return ''
    return review.classification || 'reviewed'
  }

  function staleHighLevel(status) {
    const review = reviewFor(status)
    if (review?.classification) return review.classification
    if (status.category === 'stale-clean') return 'safe to remove'
    if (status.category === 'broken') return status.worktree_exists ? 'needs repair' : 'missing worktree'
    if (status.has_uncommitted || status.has_untracked) return 'local changes'
    if (status.has_unpushed_commits) return `${status.unpushed_commits || ''} unpushed`.trim()
    if (status.git_status_unknown || status.unpushed_status_unknown) return 'git unknown'
    if (status.tmux_status && status.tmux_status !== 'none') return `tmux ${status.tmux_status}`
    if (status.editor_status === 'running') return 'editor running'
    return 'needs review'
  }

  function staleStatusClass(status) {
    if (status.category === 'stale-clean') return 'text-emerald-400'
    if (status.category === 'broken' || status.git_status_unknown) return 'text-red-400'
    if (status.has_uncommitted || status.has_untracked || status.has_unpushed_commits) return 'text-yellow-400'
    return 'text-amber-400'
  }

  function toggleDetails(name) {
    expandedRows = { ...expandedRows, [name]: !expandedRows[name] }
  }
</script>

<div class="px-3 pb-3 space-y-3 text-[11px] font-mono max-h-[62dvh] overflow-y-auto overscroll-contain touch-pan-y stale-review-scroll">
  {#if staleReviewLoading}
    <div class="text-gray-600">running full cleanup scan…</div>
  {/if}
  <div class="flex items-center justify-between gap-2">
    <div class="text-gray-500">
      {staleCleanStatuses.length} safe · {staleNeedsReviewStatuses.length} review
    </div>
    {#if staleDisplayStatuses.length > 0}
      <button
        on:click={runVisibleReviews}
        disabled={staleReviewLoading}
        class="text-purple-500 hover:text-purple-300 disabled:text-gray-700 border border-purple-900/60 hover:border-purple-700 px-2 py-0.5 transition-colors"
      >review visible</button>
    {/if}
    {#if staleCleanStatuses.length > 0}
      <button
        on:click={onPrune}
        disabled={pruningStale}
        class="text-emerald-500 hover:text-emerald-300 disabled:text-gray-700 border border-emerald-900/60 hover:border-emerald-700 px-2 py-0.5 transition-colors"
      >{pruningStale ? 'cleaning…' : pendingPruneStale ? `confirm clean ${staleCleanStatuses.length}` : `clean ${staleCleanStatuses.length}`}</button>
    {/if}
  </div>
  {#if pendingPruneStale}
    <div class="text-amber-500">Click again to remove: {staleCleanStatuses.map(s => s.session_name).join(', ')}</div>
  {/if}
  {#if staleDisplayStatuses.length > 0}
    <div class="border border-[#1e2d4a] divide-y divide-[#1e2d4a] bg-[#080c16]">
      <div class="hidden sm:grid grid-cols-[56px_minmax(0,1fr)_38px_88px_30px] gap-2 px-2 py-1 text-[9px] uppercase tracking-wider text-gray-700">
        <span>project</span>
        <span>session</span>
        <span>idle</span>
        <span>status</span>
        <span></span>
      </div>
      {#each staleDisplayStatuses as status (status.session_name)}
        {@const session = sessionByName[status.session_name] || { name: status.session_name }}
        {@const isExpanded = !!expandedRows[status.session_name]}
        {@const isDeleting = !!deletingSessions[status.session_name]}
        <div>
          <button
            on:click={() => toggleDetails(status.session_name)}
            class="w-full px-2 py-2 text-left hover:bg-[#0d1117] {isDeleting ? 'opacity-60' : ''}"
            title="tap to expand stale session details"
          >
            <div class="sm:hidden space-y-1">
              <div class="flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <div class="text-gray-400 truncate">{staleDisplayName(status)}</div>
                  <div class="text-[10px] text-gray-700 truncate">{staleProject(status)} · {session.branch || 'unknown branch'}</div>
                </div>
                <div class="text-right shrink-0">
                  <div class="text-gray-600">{staleAge(status)}</div>
                  <div class="truncate {isDeleting ? 'text-cyan-400' : staleStatusClass(status)}">{isDeleting ? 'removing…' : staleHighLevel(status)}</div>
                </div>
              </div>
              <div class="flex items-center justify-between text-[10px] text-gray-700">
                <span>tap for details + actions</span>
                <span>{isExpanded ? 'hide −' : 'details +'}</span>
              </div>
            </div>
            <div class="hidden sm:grid grid-cols-[56px_minmax(0,1fr)_38px_88px_30px] gap-2 items-center">
              <span class="text-gray-600 truncate">{staleProject(status)}</span>
              <span class="text-gray-400 truncate">{staleDisplayName(status)}</span>
              <span class="text-gray-600">{staleAge(status)}</span>
              <span class="truncate {isDeleting ? 'text-cyan-400' : staleStatusClass(status)}">{isDeleting ? 'removing…' : staleHighLevel(status)}</span>
              <span class="text-gray-700 text-right">{isExpanded ? '−' : '+'}</span>
            </div>
          </button>
          {#if isExpanded}
            <div class="px-2 pb-3 sm:pl-[66px] bg-[#0d1117] space-y-2">
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1 text-gray-600">
                <div><span class="text-gray-500">name:</span> {status.session_name}</div>
                <div><span class="text-gray-500">project:</span> {staleProject(status)}</div>
                <div><span class="text-gray-500">branch:</span> {session.branch || 'unknown'}</div>
                <div><span class="text-gray-500">category:</span> {status.category}</div>
                <div><span class="text-gray-500">tmux:</span> {status.tmux_status || 'unknown'}</div>
                <div><span class="text-gray-500">editor:</span> {status.editor_status || 'unknown'}</div>
                <div><span class="text-gray-500">worktree:</span> {status.worktree_exists ? 'exists' : 'missing'}</div>
                <div><span class="text-gray-500">git:</span> {status.git_checks_incomplete ? 'not checked' : status.git_status_unknown ? 'unknown' : 'checked'}</div>
                <div><span class="text-gray-500">unpushed:</span> {status.unpushed_status_unknown ? 'unknown' : status.has_unpushed_commits ? `${status.unpushed_commits} commits` : 'none'}</div>
                <div><span class="text-gray-500">dirty:</span> {status.has_uncommitted ? 'modified' : 'clean'}</div>
                <div><span class="text-gray-500">untracked:</span> {status.has_untracked ? 'yes' : 'no'}</div>
                <div><span class="text-gray-500">ignored:</span> {status.has_ignored ? 'yes' : 'no'}</div>
                <div><span class="text-gray-500">cleanup:</span> {status.cleanup_candidate ? 'verified' : status.potential_cleanup_candidate ? 'possible' : 'blocked'}</div>
              </div>
              <div class="text-gray-500">
                <span class="text-gray-400">reasons:</span> {staleReason(status)}
              </div>
              {#if reviewFor(status)}
                <div class="border border-purple-950/70 bg-purple-950/10 p-2 space-y-1">
                  <div class="text-purple-400">review: {reviewLabel(status)}</div>
                  {#if reviewFor(status).summary}
                    <div class="text-gray-400 leading-relaxed">{reviewFor(status).summary}</div>
                  {/if}
                  <div class="grid grid-cols-3 gap-2 text-gray-600">
                    <span>{reviewFor(status).unique_commit_count || reviewFor(status).unique_commits?.length || 0} commits</span>
                    <span>{reviewFor(status).dirty_file_count || reviewFor(status).dirty_files?.length || 0} dirty</span>
                    <span>{reviewFor(status).untracked_file_count || reviewFor(status).untracked_files?.length || 0} untracked</span>
                  </div>
                  {#if reviewFor(status).details}
                    <pre class="max-h-32 overflow-auto whitespace-pre-wrap text-gray-500 border border-[#1e2d4a] p-2">{reviewFor(status).details}</pre>
                  {/if}
                </div>
              {/if}
              {#if reviewErrorByName[status.session_name]}
                <div class="text-red-500">review failed: {reviewErrorByName[status.session_name]}</div>
              {/if}
              <div class="flex flex-wrap gap-2 pt-1">
                <button on:click={() => onOpen(status)} disabled={isDeleting} class="text-cyan-500 hover:text-cyan-300 disabled:text-gray-700 border border-cyan-950 hover:border-cyan-800 disabled:border-gray-900 px-2 py-1 sm:py-0.5">open terminal</button>
                <button on:click={() => runCleanupReview(status)} disabled={isDeleting || reviewingByName[status.session_name]} class="text-purple-500 hover:text-purple-300 disabled:text-gray-700 border border-purple-950 hover:border-purple-800 disabled:border-gray-900 px-2 py-1 sm:py-0.5">{reviewingByName[status.session_name] ? 'reviewing…' : reviewFor(status) ? 'rerun review' : 'run review'}</button>
                <button on:click={() => onReviewed(status)} disabled={isDeleting} class="text-amber-500 hover:text-amber-300 disabled:text-gray-700 border border-amber-950 hover:border-amber-800 disabled:border-gray-900 px-2 py-1 sm:py-0.5">mark reviewed</button>
                {#if session.gatepost?.logs_url}
                  <a href={session.gatepost.logs_url} target="_blank" rel="noopener noreferrer" class="text-emerald-500 hover:text-emerald-300 border border-emerald-950 hover:border-emerald-800 px-2 py-1 sm:py-0.5">gatepost logs</a>
                {/if}
                <button on:click={() => onDelete(status)} disabled={isDeleting} class="{isDeleting ? 'text-cyan-400 border-cyan-900' : pendingDelete === status.session_name ? 'text-red-300 border-red-700' : 'text-red-600 border-red-950 hover:text-red-400 hover:border-red-800'} border px-2 py-1 sm:py-0.5 disabled:cursor-wait">{isDeleting ? 'removing…' : pendingDelete === status.session_name ? 'confirm delete' : 'delete'}</button>
              </div>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .stale-review-scroll {
    -webkit-overflow-scrolling: touch;
  }
</style>
