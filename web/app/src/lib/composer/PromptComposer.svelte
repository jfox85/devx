<script>
  import { createEventDispatcher, onMount, tick } from 'svelte'
  import { sendInput } from '../../api.js'
  import { getComposerDraft, setComposerDraft } from '../stores/sessionUiState.js'

  export let sessionName
  // 'overlay' — floating panel summoned on demand (desktop, Cmd/Ctrl+K)
  // 'docked'  — persistent compact composer (mobile)
  export let variant = 'docked'
  // Docked only: whether the soft keybar is shown (toggled via ⌨ button)
  export let keysOpen = false

  const dispatch = createEventDispatcher()
  let text = ''
  let sending = false
  let error = ''
  let textareaEl
  let currentSessionName = sessionName

  onMount(() => {
    text = getComposerDraft(sessionName)
    tick().then(() => {
      autoGrow()
      if (variant === 'overlay') textareaEl?.focus()
    })
  })

  $: if (sessionName !== currentSessionName) {
    if (currentSessionName) setComposerDraft(currentSessionName, text)
    currentSessionName = sessionName
    text = getComposerDraft(sessionName)
    error = ''
    tick().then(autoGrow)
  }

  $: if (sessionName) setComposerDraft(sessionName, text)

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
    sending = true
    error = ''
    try {
      await sendInput(sessionName, payload, { submit })
      text = ''
      setComposerDraft(sessionName, '')
      await tick()
      autoGrow()
      dispatch('sent', { submit })
    } catch (e) {
      error = e.message || 'Failed to send input'
    } finally {
      sending = false
    }
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
  <div class="border-t border-[#1e2d4a] bg-[#07101f] shrink-0">
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
    {#if error}
      <div class="px-2 pb-1 text-[11px] font-mono text-red-400 truncate">{error}</div>
    {/if}
  </div>
{/if}
