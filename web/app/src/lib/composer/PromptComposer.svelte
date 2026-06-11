<script>
  import { createEventDispatcher, onMount, tick } from 'svelte'
  import { sendInput } from '../../api.js'
  import { getComposerDraft, setComposerDraft } from '../stores/sessionUiState.js'

  export let sessionName

  const dispatch = createEventDispatcher()
  let text = ''
  let sending = false
  let error = ''
  let expanded = false
  let textareaEl
  let currentSessionName = sessionName

  onMount(() => {
    text = getComposerDraft(sessionName)
    autoGrow()
  })

  $: if (sessionName !== currentSessionName) {
    if (currentSessionName) setComposerDraft(currentSessionName, text)
    currentSessionName = sessionName
    text = getComposerDraft(sessionName)
    error = ''
    tick().then(autoGrow)
  }

  $: if (sessionName) setComposerDraft(sessionName, text)

  async function setExpanded(value) {
    expanded = value
    await tick()
    autoGrow()
    dispatch('layoutchange')
  }

  function autoGrow() {
    if (!textareaEl) return
    textareaEl.style.height = 'auto'
    const max = Math.max(120, Math.floor(window.innerHeight * 0.4))
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, max) + 'px'
  }

  async function handleInput() {
    if (text && !expanded) expanded = true
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
      await setExpanded(false)
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
      send({ submit: true })
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

<div class="border-t border-[#1e2d4a] bg-[#07101f] shrink-0">
  <div class="flex items-center justify-between px-2 py-1">
    <button
      type="button"
      on:click={() => setExpanded(!expanded)}
      class="text-[10px] font-mono text-gray-500 hover:text-cyan-400"
      title="open composer"
    >composer {expanded ? '▾' : '▸'}</button>
    <div class="text-[10px] font-mono text-gray-700 truncate px-2">target: {sessionName}</div>
    <div class="hidden sm:block text-[10px] font-mono text-gray-700">⌘/Ctrl+Enter send</div>
  </div>

  {#if expanded}
    <div class="p-2 pt-1 space-y-2 border-t border-[#13213a]">
      <textarea
        bind:this={textareaEl}
        bind:value={text}
        on:input={handleInput}
        on:keydown={handleKeydown}
        on:paste={handlePaste}
        placeholder="compose outside the terminal…"
        rows="3"
        class="w-full resize-y overflow-auto bg-[#030712] border border-[#1e2d4a] focus:border-cyan-700 outline-none rounded-sm px-2 py-1.5 text-sm font-mono text-gray-200 placeholder-gray-700 min-h-[5rem] max-h-[40vh]"
        aria-label="terminal input composer"
      ></textarea>

      <div class="flex items-center gap-2">
        <button
          type="button"
          disabled={!text.trim() || sending}
          on:click={() => send({ submit: false })}
          class="px-2 py-1 text-[11px] font-mono border border-[#1e2d4a] text-gray-400 hover:text-cyan-300 hover:border-cyan-900 disabled:opacity-40 disabled:hover:text-gray-400 disabled:hover:border-[#1e2d4a]"
        >paste only</button>
        <button
          type="button"
          disabled={!text.trim() || sending}
          on:click={() => send({ submit: true })}
          class="px-2 py-1 text-[11px] font-mono border border-cyan-900/70 text-cyan-400 hover:text-cyan-200 hover:border-cyan-600 disabled:opacity-40 disabled:hover:text-cyan-400 disabled:hover:border-cyan-900/70"
        >paste + enter</button>
        {#if text}
          <button
            type="button"
            disabled={sending}
            on:click={() => { text = ''; setComposerDraft(sessionName, ''); textareaEl?.focus(); autoGrow(); dispatch('layoutchange') }}
            class="px-2 py-1 text-[11px] font-mono text-gray-600 hover:text-gray-400 disabled:opacity-40"
          >clear</button>
        {/if}
        {#if sending}
          <span class="text-[11px] font-mono text-gray-600">sending…</span>
        {/if}
        {#if error}
          <span class="text-[11px] font-mono text-red-400 truncate">{error}</span>
        {/if}
      </div>
    </div>
  {/if}
</div>
