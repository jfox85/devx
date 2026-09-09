<script>
  import { createEventDispatcher, onMount, onDestroy, tick } from 'svelte'
  import { sendInput } from '../../api.js'
  import {
    getComposerDraft, setComposerDraft, clearComposerDraft, getComposerSession,
    markComposerSending, recordComposerSend, clearComposerHistory, mergeComposerRecall,
    flushComposerDrafts, getComposerPrefs, setComposerPrefs,
  } from '../stores/sessionUiState.js'
  import { isDesktop } from '../desktopBridge.js'
  import PromptHistorySheet from './PromptHistorySheet.svelte'

  export let sessionName
  // 'overlay' — floating panel summoned on demand (desktop, Cmd/Ctrl+K)
  // 'docked'  — persistent compact composer (mobile)
  export let variant = 'docked'
  // Docked only: whether the soft keybar is shown (toggled via ⌨ button)
  export let keysOpen = false

  const dispatch = createEventDispatcher()
  // Restored synchronously (not in onMount) so the very first run of the
  // `setComposerDraft` reactive statement below sees the restored draft, not
  // an empty initial value — otherwise that first reactive write would race
  // onMount and clobber the just-restored persisted draft.
  let text = getComposerDraft(sessionName)
  let sending = false
  let error = ''
  let textareaEl
  let currentSessionName = sessionName
  let history = getComposerSession(sessionName).history
  let historyOpen = false
  let historyTriggerEl
  // Client-only privacy prefs (draft/history, both default-on) surfaced in the
  // history sheet's "local storage" footer. Read once on mount/session-switch
  // and refreshed after every change so the sheet reflects composerStorage's
  // authoritative state, matching how `history` is refreshed after a send.
  let prefs = getComposerPrefs()
  // Warn when a draft is restored while a previous send's `sending: true`
  // flag is still set (see docs/plans/2026-09-05-...): the tab may have been
  // discarded mid-send, so the send may already have reached the terminal.
  let showSendingWarning = getComposerSession(sessionName).sending && !!text

  // Flush the debounced draft write immediately when the tab is hidden or the
  // page is being torn down (mobile app-switch / discard), rather than waiting
  // for the 400ms debounce that may never fire in those cases.
  function handleVisibilityChange() {
    if (document.hidden) flushComposerDrafts(currentSessionName)
  }
  function handlePageHide() {
    flushComposerDrafts(currentSessionName)
  }

  onMount(() => {
    tick().then(() => {
      autoGrow()
      if (variant === 'overlay') textareaEl?.focus()
    })
    document.addEventListener('visibilitychange', handleVisibilityChange)
    window.addEventListener('pagehide', handlePageHide)
  })

  onDestroy(() => {
    flushComposerDrafts(currentSessionName)
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    window.removeEventListener('pagehide', handlePageHide)
  })

  $: if (sessionName !== currentSessionName) {
    if (currentSessionName) setComposerDraft(currentSessionName, text)
    currentSessionName = sessionName
    const restored = getComposerSession(sessionName)
    text = restored.draft
    history = restored.history
    showSendingWarning = restored.sending && !!restored.draft
    historyOpen = false
    error = ''
    prefs = getComposerPrefs()
    tick().then(autoGrow)
  }

  $: if (sessionName) setComposerDraft(sessionName, text)

  // Insert text at the textarea caret (or append). Used to drop an uploaded
  // image path into the composer when an image is pasted/dropped while the
  // overlay is open, instead of sending it to the terminal pane.
  // Whether the composer is actually rendered and visible (the docked variant
  // stays mounted on desktop but is hidden via CSS, so callers deciding where
  // to route inserted text need a real visibility check, not just a binding).
  export function isVisible() {
    return !!textareaEl && textareaEl.offsetParent !== null
  }

  // Whether keyboard focus is currently inside this composer (textarea, action
  // buttons, or the history sheet). Terminal.svelte consults this before its
  // deferred focusTerminal retries so they never steal focus from the docked
  // composer the user is interacting with.
  export function hasFocusWithin() {
    if (historyOpen) return true
    const active = document.activeElement
    return !!active && !!dockedRootEl?.contains(active)
  }

  let dockedRootEl = null

  export function insertText(insert) {
    const el = textareaEl
    const start = el ? el.selectionStart : text.length
    const end = el ? el.selectionEnd : text.length
    text = text.slice(0, start) + insert + text.slice(end)
    tick().then(() => {
      autoGrow()
      if (el) {
        const caret = start + insert.length
        el.focus()
        el.setSelectionRange(caret, caret)
      }
    })
  }

  function autoGrow() {
    if (!textareaEl) return
    textareaEl.style.height = 'auto'
    const maxFraction = variant === 'overlay' ? 0.5 : 0.3
    const max = Math.max(96, Math.floor(window.innerHeight * maxFraction))
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, max) + 'px'
  }

  async function handleInput() {
    await tick()
    autoGrow()
    dispatch('layoutchange')
  }

  async function send({ submit }) {
    const payload = text
    if (!payload.trim() || sending) return
    // Pin the target: `sessionName` is a reactive prop and this component stays
    // mounted across session switches (sidebar / quick switcher), so by the
    // time sendInput resolves the user may be looking at a different session.
    // Every storage write below must go to the session the prompt was sent to.
    const target = sessionName
    sending = true
    error = ''
    showSendingWarning = false
    historyOpen = false
    // Persist the in-flight flag *before* the await, alongside the still-present
    // draft: if the tab is discarded mid-send (the mobile-backgrounding case
    // this whole feature targets), a restored draft with `sending: true` warns
    // the user instead of silently inviting a duplicate send.
    markComposerSending(target, true)
    try {
      await sendInput(target, payload, { submit })
      recordComposerSend(target, payload)
      // The textarea intentionally remains editable while sendInput is in
      // flight. Only clear the draft we sent; if the user has already started
      // the next prompt, its in-memory/persisted text must survive this older
      // request completing.
      const draftUnchanged = getComposerSession(target).draft === payload
      if (draftUnchanged) clearComposerDraft(target)
      if (target === sessionName) {
        // Still on the same session: reflect the sent entry in visible history.
        history = getComposerSession(target).history
        if (text === payload && draftUnchanged) {
          text = ''
          await tick()
          autoGrow()
        }
      }
      dispatch('sent', { submit })
    } catch (e) {
      if (target === sessionName) error = e.message || 'Failed to send input'
    } finally {
      sending = false
      markComposerSending(target, false)
    }
  }

  function dismissSendingWarning() {
    showSendingWarning = false
    markComposerSending(sessionName, false)
  }

  function openHistory() {
    // Blur the textarea *before* flipping historyOpen so iOS/Android dismiss
    // the on-screen keyboard first, rather than fighting the bottom sheet's
    // entrance animation for viewport space while the keyboard is still up.
    textareaEl?.blur()
    historyOpen = true
  }

  function closeHistory() {
    historyOpen = false
    // Return focus to the trigger button on an ordinary close (not after a
    // recall, which continues focusing the textarea in handleHistorySelect).
    tick().then(() => historyTriggerEl?.focus())
  }

  function handleHistorySelect(entryText) {
    text = mergeComposerRecall(text, entryText)
    historyOpen = false
    setComposerDraft(sessionName, text)
    tick().then(() => {
      autoGrow()
      textareaEl?.focus()
      const end = text.length
      textareaEl?.setSelectionRange?.(end, end)
    })
  }

  function handleClearHistory() {
    clearComposerHistory(sessionName)
    history = []
    // Sheet stays open showing the empty state — clearing is not a close.
  }

  function handlePrefsChange(partial) {
    prefs = setComposerPrefs(partial)
    // Turning history off purges it immediately (composerStorage.setPrefs);
    // turning it back on enables future recording. Either way, refresh local
    // state from the session so the sheet reflects it right away. Turning
    // draft off purges only the *persisted* draft — text currently in the
    // textarea is left alone (memory-only behavior), so `text` is untouched.
    history = getComposerSession(sessionName).history
  }

  function handleKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      // Cmd/Ctrl+Shift+Enter = paste without submitting; Cmd/Ctrl+Enter = send
      send({ submit: !e.shiftKey })
    } else if (e.key === 'Escape' && variant === 'overlay') {
      e.preventDefault()
      dispatch('close')
    }
  }

  function handlePaste(e) {
    const files = []
    for (const item of (e.clipboardData?.items || [])) {
      if (item.kind === 'file') {
        const file = item.getAsFile()
        if (file) files.push(file)
      }
    }
    if (files.length) {
      e.preventDefault()
      dispatch('imagepaste', { files })
      return
    }
    // WKWebView (desktop shell) omits clipboard images from the DOM paste event,
    // so when the composer is focused and there are no file items but also no
    // text, ask the parent to pull the image from the native clipboard. Guard on
    // empty text/plain so a normal text paste isn't intercepted.
    const text = e.clipboardData?.getData('text/plain') || ''
    if (isDesktop() && !text) {
      e.preventDefault()
      dispatch('desktopclipboardimage')
    }
  }
</script>

{#if variant === 'overlay'}
  <!-- Desktop: transient overlay summoned by Cmd/Ctrl+K. Esc dismisses. -->
  <div
    class="absolute inset-0 z-40 flex items-end justify-center pb-10 px-6 bg-black/40"
    on:pointerdown|self={() => dispatch('close')}
  >
    <div class="w-full max-w-3xl bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-2xl overflow-hidden">
      <div class="flex items-center justify-between px-3 py-1.5 border-b border-[#13213a]">
        <div class="text-[11px] font-mono text-cyan-300">compose → {sessionName}</div>
        <div class="text-[10px] font-mono text-gray-600">⌘↵ send · ⇧⌘↵ paste · esc close</div>
      </div>
      <div class="p-2 space-y-2">
        <textarea
          bind:this={textareaEl}
          bind:value={text}
          on:input={handleInput}
          on:keydown={handleKeydown}
          on:paste={handlePaste}
          placeholder="compose a prompt or command…"
          rows="3"
          class="w-full resize-none overflow-auto bg-[#030712] border border-[#1e2d4a] focus:border-cyan-700 outline-none rounded-sm px-2 py-1.5 text-sm font-mono text-gray-200 placeholder-gray-700 min-h-[4.5rem]"
          aria-label="terminal input composer"
        ></textarea>
        <div class="flex items-center gap-2">
          <button
            type="button"
            disabled={!text.trim() || sending}
            on:click={() => send({ submit: true })}
            class="px-3 py-1 text-[11px] font-mono border border-cyan-900/70 text-cyan-300 hover:text-cyan-100 hover:border-cyan-600 disabled:opacity-40"
          >send ⌘↵</button>
          <button
            type="button"
            disabled={!text.trim() || sending}
            on:click={() => send({ submit: false })}
            class="px-3 py-1 text-[11px] font-mono border border-[#1e2d4a] text-gray-400 hover:text-cyan-300 hover:border-cyan-900 disabled:opacity-40"
          >paste only</button>
          <div class="flex-1"></div>
          {#if sending}<span class="text-[11px] font-mono text-gray-600">sending…</span>{/if}
          {#if error}<span class="text-[11px] font-mono text-red-400 truncate">{error}</span>{/if}
          <button
            type="button"
            on:click={() => dispatch('close')}
            class="px-2 py-1 text-[11px] font-mono text-gray-600 hover:text-gray-300"
          >esc</button>
        </div>
      </div>
    </div>
  </div>
{:else}
  <!-- Mobile: docked composer — THE input on touch devices. Compact single row
       that grows with content; terminal above is primarily a display surface. -->
  <div class="border-t border-[#1e2d4a] bg-[#07101f] shrink-0" bind:this={dockedRootEl}>
    <div class="flex items-end gap-1.5 p-1.5">
      <button
        type="button"
        on:click={() => dispatch('togglekeys')}
        title="show/hide terminal keys"
        class="px-2.5 py-2 text-[13px] font-mono border rounded-md shrink-0 transition-colors
          {keysOpen ? 'border-cyan-800 text-cyan-300 bg-cyan-950/30' : 'border-[#1e2d4a] text-gray-500'}"
      >⌨</button>
      <textarea
        bind:this={textareaEl}
        bind:value={text}
        on:input={handleInput}
        on:keydown={handleKeydown}
        on:paste={handlePaste}
        placeholder="message {sessionName}…"
        rows="1"
        enterkeyhint="enter"
        class="flex-1 resize-none overflow-auto bg-[#030712] border border-[#1e2d4a] focus:border-cyan-700 outline-none rounded-md px-2.5 py-2 text-sm font-mono text-gray-200 placeholder-gray-700 leading-snug"
        aria-label="terminal input composer"
      ></textarea>
      <button
        type="button"
        bind:this={historyTriggerEl}
        on:click={openHistory}
        title="prompt history"
        class="px-2.5 py-2 text-[11px] font-mono border border-[#1e2d4a] rounded-md text-gray-500 hover:text-cyan-300 active:text-cyan-200 shrink-0"
      >⏱</button>
      <button
        type="button"
        disabled={!text.trim() || sending}
        on:click={() => send({ submit: false })}
        title="paste into terminal without submitting"
        class="px-2.5 py-2 text-[11px] font-mono border border-[#1e2d4a] rounded-md text-gray-500 hover:text-cyan-300 active:text-cyan-200 disabled:opacity-40 shrink-0"
      >¶</button>
      <button
        type="button"
        disabled={!text.trim() || sending}
        on:click={() => send({ submit: true })}
        title="send to terminal"
        class="px-3.5 py-2 text-[11px] font-mono border border-cyan-900/70 rounded-md text-cyan-300 active:text-cyan-100 bg-cyan-950/30 disabled:opacity-40 shrink-0"
      >↵</button>
    </div>
    {#if showSendingWarning}
      <div class="flex items-center justify-between gap-2 px-2 pb-1.5 text-[11px] font-mono text-amber-400/80">
        <span>this may already have been sent — check the terminal before resending</span>
        <button
          type="button"
          on:click={dismissSendingWarning}
          aria-label="dismiss may-already-have-been-sent warning"
          class="shrink-0 text-amber-600 hover:text-amber-300 px-1"
        >×</button>
      </div>
    {/if}
    {#if error}
      <div class="px-2 pb-1 text-[11px] font-mono text-red-400 truncate">{error}</div>
    {/if}
  </div>
  {#if historyOpen}
    <PromptHistorySheet
      {history}
      {sessionName}
      {prefs}
      onSelect={handleHistorySelect}
      onClear={handleClearHistory}
      onClose={closeHistory}
      onPrefsChange={handlePrefsChange}
    />
  {/if}
{/if}
