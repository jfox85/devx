<script>
  // Fullscreen, in-window viewer for artifacts. Covers the whole viewport (not
  // just the artifact pane), works the same in the browser, the desktop app and
  // mobile, and lets the user step through viewable artifacts.
  import { onMount, tick } from 'svelte'
  import { renderKind, isViewable, neighborIndex, indexOfID } from './artifactKinds.js'
  import { openArtifactExternally, downloadArtifactFile } from './artifactActions.js'

  export let sessionName = ''
  export let items = []
  export let startID = ''
  export let onClose = () => {}

  // Follow the artifact by ID, not position (see indexOfID): the pane reloads
  // `items` while the viewer is open.
  let currentID = startID
  let index = 0
  $: follow(items)

  // Runs only when `items` changes. If the followed artifact was removed,
  // adopt the one now shown so later reloads track what is on screen.
  function follow(list) {
    index = indexOfID(list, currentID, index)
    if (list[index]) currentID = list[index].id
  }
  let actualSize = false
  let text = ''
  let textFor = ''
  let status = ''
  let statusTimer
  let root
  let touchStart = null

  $: current = items[index] || null
  $: kind = renderKind(current)
  $: prevIndex = neighborIndex(items, index, -1)
  $: nextIndex = neighborIndex(items, index, 1)
  $: hasNeighbors = prevIndex !== -1 && prevIndex !== index
  $: if (current && kind === 'text' && textFor !== current.id) loadText(current)

  async function loadText(a) {
    textFor = a.id
    text = 'loading…'
    try {
      const res = await fetch(a.url, { credentials: 'same-origin' })
      const body = res.ok ? await res.text() : `Failed to load (${res.status}).`
      if (textFor === a.id) text = body
    } catch {
      if (textFor === a.id) text = 'Failed to load preview.'
    }
  }

  function go(step) {
    const next = neighborIndex(items, index, step)
    if (next === -1) return
    index = next
    currentID = items[next].id
    actualSize = false
  }

  function flash(message) {
    status = message
    clearTimeout(statusTimer)
    if (message) statusTimer = setTimeout(() => { status = '' }, 4000)
  }

  async function run(action) {
    try { flash(await action(sessionName, current)) }
    catch (e) { flash(e?.message || String(e) || 'Action failed') }
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
    else if (e.key === 'ArrowLeft' && kind !== 'video') { e.preventDefault(); go(-1) }
    else if (e.key === 'ArrowRight' && kind !== 'video') { e.preventDefault(); go(1) }
  }

  // Horizontal swipe on images steps between artifacts (videos keep their
  // native scrubbing gestures; iframes swallow touches anyway).
  function touchstart(e) {
    if (e.touches.length !== 1 || actualSize) { touchStart = null; return }
    touchStart = { x: e.touches[0].clientX, y: e.touches[0].clientY }
  }
  function touchend(e) {
    if (!touchStart) return
    const t = e.changedTouches[0]
    const dx = t.clientX - touchStart.x
    const dy = t.clientY - touchStart.y
    touchStart = null
    if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) go(dx < 0 ? 1 : -1)
  }

  onMount(() => {
    // If the starting artifact is not viewable, jump to the nearest one that is.
    if (current && !isViewable(current)) {
      const n = neighborIndex(items, index, 1)
      if (n !== -1) {
        index = n
        currentID = items[n].id
      }
    }
    tick().then(() => root?.focus())
    return () => clearTimeout(statusTimer)
  })
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  bind:this={root}
  class="fixed inset-0 z-[1100] bg-black flex flex-col text-gray-200 outline-none"
  style="padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);"
  role="dialog"
  aria-modal="true"
  aria-label="artifact viewer"
  tabindex="-1"
  on:keydown={handleKeydown}
  on:touchstart|passive={touchstart}
  on:touchend={touchend}
>
  <div class="shrink-0 flex items-center gap-1 sm:gap-2 px-2 sm:px-3 py-1.5 border-b border-white/10 bg-black/60">
    <div class="flex-1 min-w-0">
      <div class="text-sm truncate" data-testid="artifact-viewer-title">{current?.title || ''}</div>
      <div class="text-[10px] font-mono text-gray-500 truncate">{current?.file || ''}{hasNeighbors ? ` · ${index + 1}/${items.length}` : ''}</div>
    </div>
    {#if kind === 'image'}
      <button class="viewer-btn" title={actualSize ? 'fit to screen' : 'show actual size'} on:click={() => actualSize = !actualSize}>{actualSize ? 'Fit' : '1:1'}</button>
    {/if}
    <button class="viewer-btn" title="open in new tab or default app" on:click={() => run(openArtifactExternally)}>Open</button>
    <button class="viewer-btn" title="download artifact" on:click={() => run(downloadArtifactFile)}>Download</button>
    <button class="viewer-btn" title="close viewer (Esc)" aria-label="close viewer" on:click={onClose}>✕</button>
  </div>

  {#if status}
    <div class="shrink-0 px-3 py-1.5 text-xs font-mono text-cyan-200 bg-cyan-950/60 border-b border-cyan-900 truncate" role="status">{status}</div>
  {/if}

  <div class="relative flex-1 min-h-0">
    {#if !current || !isViewable(current)}
      <div class="h-full flex items-center justify-center p-6 text-sm text-gray-400">No fullscreen preview for this artifact. Use Open or Download.</div>
    {:else if kind === 'image'}
      {#key current.id}
        <div class="absolute inset-0 {actualSize ? 'overflow-auto' : 'flex items-center justify-center overflow-hidden'}">
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
          <img
            src={current.url}
            alt={current.title}
            class={actualSize ? 'max-w-none cursor-zoom-out' : 'max-w-full max-h-full object-contain cursor-zoom-in'}
            on:click={() => actualSize = !actualSize}
          />
        </div>
      {/key}
    {:else if kind === 'video'}
      {#key current.id}
        <div class="absolute inset-0 flex items-center justify-center">
          <!-- svelte-ignore a11y_media_has_caption -->
          <video src={current.url} controls autoplay playsinline class="max-w-full max-h-full"></video>
        </div>
      {/key}
    {:else if kind === 'html' || kind === 'iframe'}
      <iframe title={current.title} src={current.url} sandbox="" class="absolute inset-0 w-full h-full border-0 bg-white"></iframe>
    {:else if kind === 'text'}
      <pre class="absolute inset-0 overflow-auto text-xs sm:text-sm font-mono whitespace-pre-wrap p-4 text-gray-200">{text}</pre>
    {/if}

    {#if hasNeighbors && kind !== 'video'}
      <button class="nav-btn left-1 sm:left-3" aria-label="previous artifact" on:click={() => go(-1)}>‹</button>
      <button class="nav-btn right-1 sm:right-3" aria-label="next artifact" on:click={() => go(1)}>›</button>
    {/if}
  </div>

  {#if hasNeighbors && kind === 'video'}
    <div class="shrink-0 flex justify-center gap-6 py-2 border-t border-white/10">
      <button class="viewer-btn" aria-label="previous artifact" on:click={() => go(-1)}>‹ Prev</button>
      <button class="viewer-btn" aria-label="next artifact" on:click={() => go(1)}>Next ›</button>
    </div>
  {/if}
</div>

<style>
  .viewer-btn {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    padding: 6px 10px;
    min-height: 32px;
    border-radius: 4px;
    color: #d1d5db;
    border: 1px solid rgba(255, 255, 255, 0.12);
    background: rgba(255, 255, 255, 0.04);
    white-space: nowrap;
  }
  .viewer-btn:hover { color: #67e8f9; border-color: #0e7490; }
  .nav-btn {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    width: 40px;
    height: 56px;
    font-size: 28px;
    line-height: 1;
    border-radius: 6px;
    color: #e5e7eb;
    background: rgba(0, 0, 0, 0.45);
    border: 1px solid rgba(255, 255, 255, 0.1);
  }
  .nav-btn:hover { color: #67e8f9; }
</style>
