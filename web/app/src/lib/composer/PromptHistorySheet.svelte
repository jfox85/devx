<!-- web/app/src/lib/composer/PromptHistorySheet.svelte -->
<!-- Mobile bottom sheet listing recent sent prompts for one session, newest
     first. Pure presentation: no storage import — the caller (PromptComposer)
     owns the composerStorage-backed history array and passes callbacks for
     select/clear/close. Follows the NewSessionModal/UsageDetailModal modal
     conventions (fixed inset overlay, dark palette, border #1e2d4a, font
     mono) but anchored to the bottom (`items-end`) like a native sheet. -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import { relativeHistoryTime } from './composerFormat.js'

  export let history = []       // [{ text, at }], newest first
  export let sessionName = ''
  export let onSelect = () => {}
  export let onClear = () => {}
  export let onClose = () => {}
  // Client-only privacy prefs ({ draft, history }, both default true) and the
  // callback to change them. This component never imports storage itself —
  // PromptComposer owns sessionUiState's getComposerPrefs/setComposerPrefs and
  // passes the current values + a setter down, same pattern as history.
  export let prefs = { draft: true, history: true }
  export let onPrefsChange = () => {}

  let sheetEl
  let closeButtonEl

  // Two-step "clear history" confirmation: the first tap only arms the
  // control (label flips to a danger-toned "[confirm clear?]"); a second tap
  // within CLEAR_ARM_MS actually clears. Disarms on a 5s timeout, and
  // unconditionally on destroy (sheet close unmounts this component via the
  // #if in PromptComposer) so a stray armed state never lingers across opens.
  const CLEAR_ARM_MS = 5000
  let clearArmed = false
  let clearTimer = null

  function disarmClear() {
    clearArmed = false
    if (clearTimer !== null) {
      clearTimeout(clearTimer)
      clearTimer = null
    }
  }

  function handleClearClick() {
    if (!clearArmed) {
      clearArmed = true
      clearTimer = setTimeout(disarmClear, CLEAR_ARM_MS)
      return
    }
    disarmClear()
    onClear()
  }

  onMount(() => {
    closeButtonEl?.focus()
  })

  onDestroy(() => {
    disarmClear()
  })

  // Truncate a preview to at most 3 lines, collapsing longer text with an
  // ellipsis so entries stay scannable in a compact list.
  function preview(text) {
    const lines = String(text ?? '').split('\n')
    const shown = lines.slice(0, 3)
    const truncated = lines.length > 3
    let joined = shown.join('\n')
    if (joined.length > 240) joined = joined.slice(0, 240) + '…'
    return truncated ? joined + '…' : joined
  }

  function focusableControls() {
    return Array.from(sheetEl?.querySelectorAll('button, input, [tabindex]:not([tabindex="-1"])') || [])
      .filter(el => !el.disabled && el.offsetParent !== null)
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault()
      disarmClear()
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

  function selectEntry(entry) {
    onSelect(entry.text)
  }
</script>

<!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
<div
  class="fixed inset-0 z-50 flex items-end justify-center bg-black/70 p-0"
  role="dialog" aria-modal="true" aria-label={`Prompt history for ${sessionName}`} tabindex="-1"
  bind:this={sheetEl}
  on:click|self={onClose}
  on:keydown={handleKeydown}
>
  <div class="w-full max-h-[70dvh] overflow-y-auto bg-[#0d1117] border-t border-[#1e2d4a] pb-[env(safe-area-inset-bottom)]">
    <div class="flex items-center justify-between px-4 py-2 border-b border-[#1e2d4a] sticky top-0 bg-[#0d1117]">
      <span class="text-cyan-400 text-xs font-mono font-bold tracking-widest truncate">prompt history · {sessionName}</span>
      <div class="flex items-center gap-2 shrink-0">
        <button
          type="button"
          on:click={handleClearClick}
          disabled={history.length === 0}
          class="font-mono text-[11px] px-1.5 py-0.5 disabled:opacity-40 {clearArmed ? 'text-red-400 font-bold' : 'text-gray-500 hover:text-red-400'}"
        >{clearArmed ? '[confirm clear?]' : '[clear]'}</button>
        <button
          bind:this={closeButtonEl}
          type="button"
          on:click={() => { disarmClear(); onClose() }}
          aria-label="Close"
          class="text-gray-600 hover:text-gray-400 font-mono text-xs px-1.5 py-0.5"
        >×</button>
      </div>
    </div>

    {#if history.length === 0}
      <p class="px-4 py-6 text-gray-600 text-xs font-mono text-center">no prompt history yet</p>
    {:else}
      <ul class="divide-y divide-[#1e2d4a]">
        {#each history as entry (entry.at)}
          <li>
            <button
              type="button"
              on:click={() => selectEntry(entry)}
              aria-label={`${preview(entry.text)} — ${relativeHistoryTime(entry.at)}`}
              class="w-full text-left px-4 py-3 min-h-11 hover:bg-[#0d1a2e] active:bg-[#132238] transition-colors"
            >
              <div class="flex items-baseline justify-between gap-2">
                <span class="text-gray-700 text-[10px] font-mono shrink-0">{relativeHistoryTime(entry.at)}</span>
              </div>
              <p class="text-gray-300 text-[12px] font-mono whitespace-pre-wrap break-words leading-snug mt-0.5">{preview(entry.text)}</p>
            </button>
          </li>
        {/each}
      </ul>
    {/if}

    <div class="border-t border-[#1e2d4a] px-4 py-2.5 space-y-1.5">
      <p class="text-gray-600 text-[10px] font-mono tracking-widest uppercase">local storage</p>
      <label class="flex items-center gap-2 text-[11px] font-mono text-gray-400">
        <input
          type="checkbox"
          checked={prefs.draft}
          on:change={(e) => onPrefsChange({ draft: e.currentTarget.checked })}
          aria-label="save drafts on this device"
          class="accent-cyan-600"
        />
        <span>save drafts on this device</span>
      </label>
      <label class="flex items-center gap-2 text-[11px] font-mono text-gray-400">
        <input
          type="checkbox"
          checked={prefs.history}
          on:change={(e) => onPrefsChange({ history: e.currentTarget.checked })}
          aria-label="keep sent prompt history"
          class="accent-cyan-600"
        />
        <span>keep sent prompt history</span>
      </label>
      <p class="text-gray-700 text-[10px] font-mono">saved only in this browser</p>
    </div>
  </div>
</div>
