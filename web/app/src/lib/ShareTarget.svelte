<script>
  import { onMount } from 'svelte'
  import { listSessions, getShareIntent, commitShareIntent } from '../api.js'

  export let token
  export let onCancel = () => {}
  export let onCreated = () => {}

  let sessions = []
  let intent = null
  let selectedSession = ''
  let title = ''
  let type = 'document'
  let format = 'md'
  let retention = 'session'
  let tags = ''
  let loading = true
  let saving = false
  let error = ''

  const types = ['document', 'plan', 'report', 'screenshot', 'recording', 'log', 'diff', 'other']

  $: hasText = !!(intent?.text || intent?.url)
  $: hasFiles = (intent?.files || []).length > 0

  function defaultTitle(intent) {
    if (!intent) return ''
    if (intent.title) return intent.title
    if (intent.files?.length === 1) return intent.files[0].name.replace(/\.[^.]+$/, '')
    if (intent.files?.length > 1) return `${intent.files.length} shared files`
    if (intent.url) return 'Shared link'
    return 'Shared text'
  }

  async function load() {
    loading = true
    try {
      const [items, shared] = await Promise.all([listSessions(), getShareIntent(token)])
      sessions = items
      intent = shared
      title = defaultTitle(shared)
      selectedSession = items[0]?.name || ''
      error = ''
    } catch (e) {
      error = e.message || 'Failed to load shared content'
    } finally {
      loading = false
    }
  }

  async function submit() {
    if (!selectedSession) { error = 'Choose a session'; return }
    saving = true
    try {
      const artifacts = await commitShareIntent(token, { session: selectedSession, title, type, format, retention, tags })
      error = ''
      onCreated({ session: selectedSession, artifacts })
    } catch (e) {
      error = e.message || 'Failed to create artifact'
    } finally {
      saving = false
    }
  }

  onMount(load)
</script>

<div class="fixed inset-0 z-[1200] bg-[#0a0e1a] text-gray-200 flex items-center justify-center p-4">
  <div class="w-full max-w-2xl bg-[#0b1020] border border-[#1e2d4a] rounded-xl shadow-2xl overflow-hidden">
    <div class="flex items-center justify-between border-b border-[#1e2d4a] px-4 py-3">
      <div>
        <div class="text-sm font-mono text-cyan-300">Create artifact from shared content</div>
        <div class="text-[11px] font-mono text-gray-500">Choose a DevX session and artifact metadata.</div>
      </div>
      <button class="text-xs font-mono text-gray-500 hover:text-cyan-300" on:click={onCancel}>[close]</button>
    </div>

    {#if loading}
      <div class="p-6 text-sm font-mono text-gray-500">loading shared content…</div>
    {:else}
      <div class="p-4 space-y-4">
        {#if error}
          <div class="text-sm text-red-300 bg-red-950/40 border border-red-900 rounded px-3 py-2">{error}</div>
        {/if}

        {#if intent}
          <div class="rounded border border-[#1e2d4a] bg-black/20 p-3 space-y-2">
            <div class="text-[11px] font-mono text-gray-500 uppercase tracking-wide">Shared payload</div>
            {#if hasText}
              <pre class="max-h-32 overflow-auto whitespace-pre-wrap text-xs font-mono text-gray-300">{[intent.text, intent.url].filter(Boolean).join('\n\n')}</pre>
            {/if}
            {#if hasFiles}
              <div class="space-y-1">
                {#each intent.files as file}
                  <div class="text-xs font-mono text-gray-300 flex justify-between gap-3">
                    <span class="truncate">{file.name}</span>
                    <span class="text-gray-600 shrink-0">{Math.ceil(file.size / 1024)} KB</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}

        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block text-xs font-mono text-gray-400">Session
            <select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={selectedSession}>
              {#each sessions as session}
                <option value={session.name}>{session.display_name || session.name}</option>
              {/each}
            </select>
          </label>
          <label class="block text-xs font-mono text-gray-400">Artifact type
            <select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={type}>
              {#each types as t}<option value={t}>{t}</option>{/each}
            </select>
          </label>
          <label class="block text-xs font-mono text-gray-400 sm:col-span-2">Title
            <input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={title} />
          </label>
          {#if hasText}
            <label class="block text-xs font-mono text-gray-400">Text format
              <select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={format}>
                <option value="md">Markdown</option>
                <option value="txt">Plain text</option>
                <option value="html">HTML</option>
                <option value="log">Log</option>
              </select>
            </label>
          {/if}
          <label class="block text-xs font-mono text-gray-400">Retention
            <select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={retention}>
              <option value="session">session</option>
              <option value="archive">archive</option>
            </select>
          </label>
          <label class="block text-xs font-mono text-gray-400 sm:col-span-2">Tags
            <input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={tags} placeholder="shared,mobile" />
          </label>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button class="px-3 py-2 text-xs font-mono text-gray-400 hover:text-cyan-300" on:click={onCancel}>Cancel</button>
          <button class="px-3 py-2 text-xs font-mono text-cyan-200 bg-cyan-950/50 border border-cyan-900 rounded hover:border-cyan-500 disabled:opacity-50" disabled={saving || !selectedSession} on:click={submit}>{saving ? 'Creating…' : 'Create artifact'}</button>
        </div>
      </div>
    {/if}
  </div>
</div>
