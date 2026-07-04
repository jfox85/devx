<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { approveAsk, denyAsk, listPendingAsks } from '../api.js'

  let pending = []
  let busy = false
  let error = ''
  let timer
  let dialog
  let denyButton
  let focusedID = ''

  $: current = pending[0]
  $: if (current && current.id !== focusedID) {
    focusedID = current.id
    focusDialog()
  }

  async function focusDialog() {
    await tick()
    denyButton?.focus()
  }

  function handleKeydown(event) {
    if (event.key === 'Escape') {
      event.preventDefault()
      deny()
      return
    }
    if (event.key !== 'Tab' || !dialog) return
    const focusable = Array.from(dialog.querySelectorAll('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'))
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  async function load() {
    if (busy) return
    try {
      pending = await listPendingAsks()
      error = ''
    } catch (e) {
      error = e.message || String(e)
    }
  }

  async function approve(always = false) {
    if (!current) return
    busy = true
    try {
      await approveAsk(current.id, { always })
      await load()
    } catch (e) {
      error = e.message || String(e)
    } finally {
      busy = false
    }
  }

  async function deny() {
    if (!current) return
    busy = true
    try {
      await denyAsk(current.id)
      await load()
    } catch (e) {
      error = e.message || String(e)
    } finally {
      busy = false
    }
  }

  onMount(() => {
    load()
    timer = setInterval(load, 5000)
  })

  onDestroy(() => clearInterval(timer))
</script>

{#if current}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
    <div bind:this={dialog} class="w-full max-w-lg rounded-xl border border-amber-400/40 bg-[#111827] p-5 shadow-2xl text-gray-100" role="dialog" aria-modal="true" aria-labelledby="ask-approval-title" tabindex="-1" on:keydown={handleKeydown}>
      <div class="mb-3 text-xs font-mono uppercase tracking-widest text-amber-300">Pending DevX ask approval</div>
      <h2 id="ask-approval-title" class="text-lg font-semibold mb-3">Allow responder agent?</h2>
      <p class="text-sm text-gray-300 mb-3">
        Session <span class="font-mono text-cyan-300">{current.from_session || 'unknown'}</span>
        wants to ask <span class="font-mono text-cyan-300">{current.to_session}</span>:
      </p>
      <blockquote class="rounded-lg border border-gray-700 bg-black/30 p-3 text-sm whitespace-pre-wrap">{current.question}</blockquote>
      <p class="mt-3 text-xs text-gray-400">Approving runs the configured responder command in the target worktree.</p>
      {#if error}<p class="mt-3 text-sm text-red-300">{error}</p>{/if}
      <div class="mt-5 flex justify-end gap-3">
        <button bind:this={denyButton} class="rounded-md border border-gray-600 px-4 py-2 text-sm hover:bg-gray-800 disabled:opacity-50" disabled={busy} on:click={deny}>Deny</button>
        <button class="rounded-md border border-amber-400/60 px-4 py-2 text-sm text-amber-200 hover:bg-amber-950/30 disabled:opacity-50" disabled={busy} on:click={() => approve(false)}>Approve once</button>
        <button class="rounded-md bg-amber-400 px-4 py-2 text-sm font-semibold text-black hover:bg-amber-300 disabled:opacity-50" disabled={busy} on:click={() => approve(true)}>Approve always</button>
      </div>
    </div>
  </div>
{/if}
