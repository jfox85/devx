<script>
  import { onMount } from 'svelte'
  import { listArtifacts, uploadArtifacts, createTextArtifact, renameArtifact, archiveArtifact, removeArtifact } from '../../api.js'

  export let session
  export let selectedArtifactID = null
  export let pasteArtifactNonce = 0
  export let fullScreen = false
  export let onToggleFullScreen = () => {}
  export let onInsert = () => {}
  export let onClose = () => {}

  let artifacts = []
  let selected = null
  let fileInput
  let loading = false
  let previewLoading = false
  let error = ''
  let textPreview = ''
  let htmlPreview = ''
  let jsxPreviewMode = 'preview'
  let dragOver = false
  let listCollapsed = false
  let listHeight = 220
  let resizingList = false
  let lastAppliedSelectedArtifactID = null
  let wasFullScreen = false

  let textModalOpen = false
  let textModalTitle = ''
  let textModalText = ''
  let textModalFormat = 'md'
  let textModalTags = ''
  let textModalRetention = 'session'

  let renameModalOpen = false
  let renameTitle = ''
  let renameSummary = ''
  let renameTags = ''
  let renameRetention = 'session'

  let lastPasteArtifactNonce = pasteArtifactNonce

  $: selectedURL = selected?.url || ''
  $: selectedExt = selected?.file?.split('.').pop()?.toLowerCase() || ''
  $: if (fullScreen && !wasFullScreen) {
    listCollapsed = true
    wasFullScreen = true
  } else if (!fullScreen && wasFullScreen) {
    wasFullScreen = false
  }

  $: if (selectedArtifactID && selectedArtifactID !== lastAppliedSelectedArtifactID && artifacts.length) {
    lastAppliedSelectedArtifactID = selectedArtifactID
    const match = artifacts.find(a => a.id === selectedArtifactID)
    if (match) select(match)
  }

  $: if (pasteArtifactNonce && pasteArtifactNonce !== lastPasteArtifactNonce) {
    lastPasteArtifactNonce = pasteArtifactNonce
    openTextModal()
  }

  async function load() {
    if (!session?.name) return
    loading = true
    try {
      artifacts = await listArtifacts(session.name)
      if (selectedArtifactID) selected = artifacts.find(a => a.id === selectedArtifactID) || selected
      if (!selected && artifacts.length) selected = artifacts.find(a => a.focus) || artifacts[0]
      if (selected) selected = artifacts.find(a => a.id === selected.id) || artifacts[0] || null
      error = ''
      await loadPreview()
    } catch (e) {
      error = e.message || 'Failed to load artifacts'
    } finally {
      loading = false
    }
  }

  function renderKind(a = selected) {
    if (!a) return 'other'
    const ext = a.file?.split('.').pop()?.toLowerCase() || ''
    if (a.type === 'screenshot' || ['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(ext)) return 'image'
    if (a.type === 'recording' || ['webm', 'mp4', 'mov'].includes(ext)) return 'video'
    if (['jsx', 'tsx'].includes(ext)) return 'jsx'
    if (['log', 'diff'].includes(a.type) || ['md', 'txt', 'log', 'diff', 'patch', 'js', 'ts', 'css', 'json'].includes(ext)) return 'text'
    if (['html', 'htm'].includes(ext) || ['plan', 'report'].includes(a.type)) return 'html'
    if (['pdf'].includes(ext) || ['document'].includes(a.type)) return 'iframe'
    return 'other'
  }

  function previewKind(a = selected) {
    const kind = renderKind(a)
    if ((kind === 'text' || kind === 'iframe') && looksLikeJSX(textPreview)) return 'jsx'
    return kind
  }

  async function loadPreview() {
    textPreview = ''
    htmlPreview = ''
    if (!selected) return
    previewLoading = true
    const kind = renderKind(selected)
    try {
      if (kind === 'text' || kind === 'jsx') {
        const res = await fetch(selected.url, { credentials: 'same-origin' })
        textPreview = await res.text()
      }
    } catch {
      if (kind === 'text') textPreview = 'Failed to load preview.'
      if (kind === 'html') htmlPreview = '<!doctype html><body style="background:#0f1117;color:#e5e7eb;font-family:sans-serif">Failed to load preview.</body>'
    } finally {
      previewLoading = false
    }
  }

  async function select(a) {
    selected = a
    error = ''
    lastAppliedSelectedArtifactID = selectedArtifactID
    await loadPreview()
  }

  async function handleFiles(files) {
    if (!files?.length) return
    try {
      const added = await uploadArtifacts(session.name, Array.from(files))
      artifacts = await listArtifacts(session.name)
      selected = artifacts.find(a => a.id === added[0]?.id) || artifacts[0] || null
      listCollapsed = false
      await loadPreview()
      error = ''
    } catch (e) { error = e.message || 'Upload failed' }
  }

  function openTextModal(defaultText = '') {
    textModalTitle = ''
    textModalText = defaultText
    textModalFormat = looksLikeJSX(defaultText) ? 'jsx' : looksLikeHTML(defaultText) ? 'html' : 'md'
    textModalTags = ''
    textModalRetention = 'session'
    textModalOpen = true
  }

  function looksLikeHTML(text) { return /<\s*(html|body|div|p|h1|h2|section|article|span|table|ul|ol|pre|code)[\s>]/i.test(text || '') }
  function looksLikeJSX(text) { return /export\s+default|function\s+\w+\s*\([^)]*\)\s*{[\s\S]*return\s*\(|const\s+[A-Z][A-Za-z0-9_$]*\s*=\s*(\([^)]*\)|[^=]+)\s*=>|className=|<>|<\/[A-Z][A-Za-z0-9]*>|<[A-Z][A-Za-z0-9]*[\s/>]/.test(text || '') }
  function defaultTitle() {
    const d = new Date(); const pad = n => String(n).padStart(2, '0')
    return `Pasted text ${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  }

  async function submitTextModal() {
    const text = textModalText.trim()
    if (!text) { error = 'Text artifact content is required'; return }
    try {
      const title = textModalTitle.trim() || defaultTitle()
      const type = ['html', 'md', 'jsx', 'tsx'].includes(textModalFormat) ? 'document' : 'log'
      const added = await createTextArtifact(session.name, { title, text, format: textModalFormat, tags: textModalTags, retention: textModalRetention, type })
      artifacts = await listArtifacts(session.name)
      selected = artifacts.find(a => a.id === added[0]?.id) || artifacts[0] || null
      textModalOpen = false
      listCollapsed = false
      await loadPreview()
      error = ''
    } catch (e) { error = e.message || 'Create failed' }
  }
  function cancelTextModal() { textModalOpen = false }
  function modalKeydown(e) {
    if (e.key === 'Escape') { e.preventDefault(); cancelTextModal(); renameModalOpen = false }
    else if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); textModalOpen ? submitTextModal() : submitRenameModal() }
  }

  function openRenameModal() {
    if (!selected) return
    renameTitle = selected.title || ''
    renameSummary = selected.summary || ''
    renameTags = (selected.tags || []).join(', ')
    renameRetention = selected.retention || 'session'
    renameModalOpen = true
  }
  async function submitRenameModal() {
    if (!selected) return
    try {
      selected = await renameArtifact(session.name, selected.id, { title: renameTitle.trim() || selected.title, summary: renameSummary, tags: renameTags.split(',').map(t => t.trim()).filter(Boolean), retention: renameRetention })
      artifacts = await listArtifacts(session.name)
      selected = artifacts.find(a => a.id === selected.id) || selected
      renameModalOpen = false
      error = ''
    } catch (e) { error = e.message || 'Rename failed' }
  }

  async function archiveSelected() {
    if (!selected) return
    try { selected = await archiveArtifact(session.name, selected.id); artifacts = await listArtifacts(session.name); error = '' }
    catch (e) { error = e.message || 'Archive failed' }
  }
  async function removeSelected() {
    if (!selected || !confirm(`Remove artifact "${selected.title}"?`)) return
    try { await removeArtifact(session.name, selected.id); artifacts = await listArtifacts(session.name); selected = artifacts[0] || null; await loadPreview(); error = '' }
    catch (e) { error = e.message || 'Remove failed' }
  }

  function jsxPreviewSrcdoc(sourceText) {
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <style>
    html, body, #root { min-height: 100%; margin: 0; }
    body { background: #f8fafc; color: #0f172a; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    #error { white-space: pre-wrap; color: #fecaca; background: #450a0a; padding: 16px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  </style>
</head>
<body>
  <div id="root"></div><pre id="error" hidden></pre>
  <script src="https://cdn.tailwindcss.com"><\/script>
  <script src="https://unpkg.com/react@18/umd/react.development.js"><\/script>
  <script src="https://unpkg.com/react-dom@18/umd/react-dom.development.js"><\/script>
  <script src="https://unpkg.com/@babel/standalone/babel.min.js"><\/script>
  <script type="application/json" id="source">${JSON.stringify(sourceText || '').replace(/</g, '\\u003c')}<\/script>
  <script>
    const showError = (err) => { const el = document.getElementById('error'); el.hidden = false; el.textContent = String(err && (err.stack || err.message) || err); };
    const hasComponentSyntax = (s) => /export\\s+default|function\\s+[A-Z][A-Za-z0-9_$]*\\s*\\(|(?:const|let|var)\\s+[A-Z][A-Za-z0-9_$]*\\s*=|class\\s+[A-Z][A-Za-z0-9_$]*\\b|className=|<\\/?[A-Z][A-Za-z0-9]*/.test(s || '');
    const extractCandidateSource = (raw) => {
      const fence = String.fromCharCode(96, 96, 96);
      const lines = String(raw || '').split(/\\r?\\n/);
      const blocks = [];
      let inFence = false, lang = '', buf = [];
      for (const line of lines) {
        const trimmed = line.trim();
        if (trimmed.startsWith(fence)) {
          if (inFence) { blocks.push({ lang, code: buf.join('\\n') }); inFence = false; lang = ''; buf = []; }
          else { inFence = true; lang = trimmed.slice(3).trim().toLowerCase(); buf = []; }
        } else if (inFence) buf.push(line);
      }
      const codeLang = (b) => /^(jsx|tsx|javascript|js|typescript|ts)$/.test(b.lang);
      const preferred = blocks.find(b => codeLang(b) && hasComponentSyntax(b.code)) || blocks.find(b => hasComponentSyntax(b.code));
      if (preferred) return preferred.code.trim();
      let fallback = String(raw || '').trim();
      if (fallback.startsWith(fence)) {
        fallback = fallback.slice(fence.length).replace(/^[^\\n]*\\n?/, '');
        const end = fallback.lastIndexOf(fence);
        if (end >= 0) fallback = fallback.slice(0, end);
      }
      return fallback.trim();
    };
    try {
      let source = extractCandidateSource(JSON.parse(document.getElementById('source').textContent));
      source = source.replace(/^\\s*import\\s+React\\s*,\\s*{([^}]+)}\\s+from\\s+['"]react['"];?\\s*$/gm, 'const {$1} = React;');
      source = source.replace(/^\\s*import\\s+{([^}]+)}\\s+from\\s+['"]react['"];?\\s*$/gm, 'const {$1} = React;');
      source = source.replace(/^\\s*import\\s+React\\s+from\\s+['"]react['"];?\\s*$/gm, '');
      source = source.replace(/^\\s*import[^;]+;?\\s*$/gm, '');
      let componentExpr = '';
      const namedDefaultFunction = /export\\s+default\\s+function\\s+([A-Za-z_$][\\w$]*)\\s*\\(/.exec(source);
      if (namedDefaultFunction) {
        source = source.replace(namedDefaultFunction[0], 'function ' + namedDefaultFunction[1] + '(');
        componentExpr = namedDefaultFunction[1];
      } else if (/export\\s+default\\s+function\\s*\\(/.test(source)) {
        source = source.replace(/export\\s+default\\s+function\\s*\\(/, 'const __DevxComponent = function(');
        componentExpr = '__DevxComponent';
      } else {
        const namedDefault = /export\\s+default\\s+([A-Za-z_$][\\w$]*)\\s*;?/.exec(source);
        if (namedDefault) {
          source = source.replace(namedDefault[0], '');
          componentExpr = namedDefault[1];
        } else if (/export\\s+default\\s+/.test(source)) {
          source = source.replace(/export\\s+default\\s+/, 'const __DevxComponent = ');
          componentExpr = '__DevxComponent';
        }
      }
      source = source.replace(/export\\s+(function|const|let|var|class)\\s+/g, '$1 ');
      if (!componentExpr) {
        const names = [];
        const componentPattern = /(?:function|const|let|var|class)\\s+([A-Z][A-Za-z0-9_$]*)\\b/g;
        let match;
        while ((match = componentPattern.exec(source))) {
          if (!['React', 'T'].includes(match[1])) names.push(match[1]);
        }
        componentExpr = names.includes('App') ? 'App' : names[names.length - 1];
      }
      const transformed = Babel.transform(source, { filename: 'artifact.tsx', presets: ['typescript', 'react'] }).code;
      const C = componentExpr ? new Function('React', 'ReactDOM', transformed + '\\n;return (typeof ' + componentExpr + ' !== "undefined" ? ' + componentExpr + ' : undefined);')(React, ReactDOM) : null;
      if (!C) throw new Error('No React component found (detected: ' + (componentExpr || 'none') + '). Export a default component or define App/CapitalizedComponent.');
      ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(C));
    } catch (err) { showError(err); }
  <\/script>
</body>
</html>`
  }

  function insertSelected() { if (selected) onInsert(selected.path || `.artifacts/${selected.file}`) }
  function handleDrop(e) { e.preventDefault(); dragOver = false; handleFiles(e.dataTransfer?.files) }
  function handlePaste(e) {
    if (textModalOpen) return
    const items = Array.from(e.clipboardData?.items || [])
    const files = items.filter(i => i.kind === 'file').map(i => i.getAsFile()).filter(Boolean)
    if (files.length) { e.preventDefault(); handleFiles(files); return }
    const text = e.clipboardData?.getData('text/plain')
    if (text) { e.preventDefault(); openTextModal(text) }
  }

  function startResizeList(e) {
    resizingList = true
    const startY = e.clientY
    const startH = listHeight
    const move = ev => { listHeight = Math.max(120, Math.min(520, startH + ev.clientY - startY)) }
    const up = () => { resizingList = false; window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  onMount(load)
</script>

<div class="h-full min-h-0 flex flex-col bg-[#0b1020] text-gray-200 relative outline-none border-l border-[#1e2d4a]" tabindex="0" role="application" aria-label="artifacts panel" on:paste={handlePaste} on:dragenter={(e) => { if (Array.from(e.dataTransfer?.items || []).some(i => i.kind === 'file')) dragOver = true }} on:dragover={(e) => e.preventDefault()} on:dragleave={() => { dragOver = false }} on:drop={handleDrop}>
  <div class="h-9 shrink-0 flex items-center gap-2 border-b border-[#1e2d4a] px-2 bg-[#0a0e1a]">
    <div class="text-xs font-mono text-cyan-300 flex-1 truncate">artifacts {artifacts.length ? `(${artifacts.length})` : ''}</div>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="show or hide artifact list" on:click={() => { listCollapsed = !listCollapsed }}>{listCollapsed ? '[show list]' : '[hide list]'}</button>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="use full browser window" on:click={onToggleFullScreen}>{fullScreen ? '[exit full]' : '[full screen]'}</button>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="upload artifact files" on:click={() => fileInput?.click()}>[upload]</button>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="create artifact from pasted text" on:click={() => openTextModal()}>[new]</button>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="refresh artifact list" on:click={load}>[refresh]</button>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="close artifacts and clear focus" on:click={onClose}>[close]</button>
    <input bind:this={fileInput} class="hidden" type="file" multiple on:change={(e) => { handleFiles(e.target.files); e.target.value = '' }} />
  </div>

  {#if dragOver}
    <div class="absolute inset-0 z-20 bg-cyan-950/70 border-2 border-cyan-500 border-dashed flex flex-col items-center justify-center pointer-events-none">
      <div class="text-cyan-300 font-mono text-lg">drop files to create artifacts</div>
      <div class="text-cyan-600 font-mono text-[11px]">html · md · jsx · images · video · logs · pdf</div>
    </div>
  {/if}

  {#if error}
    <div class="text-xs text-red-300 bg-red-950/40 border-b border-red-900 px-3 py-2 flex items-center gap-2"><span class="flex-1">{error}</span><button on:click={() => error = ''}>×</button></div>
  {/if}

  {#if !listCollapsed}
    <div class="shrink-0 overflow-auto border-b border-[#1e2d4a]" style="height: {artifacts.length ? listHeight + 'px' : 'auto'}">
      {#if loading}
        <div class="p-3 text-xs font-mono text-gray-500">loading…</div>
      {:else if artifacts.length === 0}
        <div class="p-3 text-xs font-mono text-gray-500">No artifacts — drop files, paste text, or click [upload]/[new].</div>
      {:else}
        {#each artifacts as a}
          <button class="w-full text-left px-3 py-2 border-b border-[#111a2e] hover:bg-cyan-950/30 {selected?.id === a.id ? 'bg-cyan-950/40' : ''}" on:click={() => select(a)}>
            <div class="text-xs text-gray-100 truncate">{a.title}</div>
            <div class="text-[10px] font-mono text-gray-500 truncate">{a.type} · {a.file}</div>
          </button>
        {/each}
      {/if}
    </div>
    {#if artifacts.length > 0}
      <button class="h-1.5 shrink-0 bg-[#111a2e] hover:bg-cyan-900 cursor-row-resize" class:bg-cyan-800={resizingList} title="drag to resize artifact list" on:mousedown={startResizeList}></button>
    {/if}
  {/if}

  {#if selected}
    <div class="h-9 shrink-0 flex items-center gap-2 border-b border-[#1e2d4a] px-3 bg-[#0a0e1a]">
      <div class="flex-1 min-w-0"><div class="text-xs truncate">{selected.title}</div><div class="text-[10px] font-mono text-gray-500 truncate">{selected.agent || 'unknown'} · {selected.retention || 'session'}</div></div>
      {#if previewKind(selected) === 'jsx'}
        <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="toggle JSX preview/code" on:click={() => jsxPreviewMode = jsxPreviewMode === 'preview' ? 'code' : 'preview'}>{jsxPreviewMode === 'preview' ? '[code]' : '[preview]'}</button>
      {/if}
      <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" on:click={insertSelected}>[insert]</button>
      <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" on:click={openRenameModal}>[edit]</button>
      <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" on:click={archiveSelected}>[archive]</button>
      <button class="text-[11px] font-mono text-red-700 hover:text-red-400" on:click={removeSelected}>[remove]</button>
    </div>
    <div class="flex-1 min-h-0 overflow-auto bg-[#0f1117] border-t border-[#101827] relative">
      {#if previewLoading}<div class="absolute top-2 right-3 z-10 text-[11px] font-mono text-gray-500">loading preview…</div>{/if}
      {#if previewKind(selected) === 'image'}
        <div class="h-full flex items-center justify-center p-4"><img src={selectedURL} alt={selected.title} class="max-w-full max-h-full object-contain" /></div>
      {:else if previewKind(selected) === 'video'}
        <div class="p-4"><video src={selectedURL} controls class="max-w-full rounded border border-[#1e2d4a]"></video></div>
      {:else if previewKind(selected) === 'text'}
        <pre class="text-xs font-mono whitespace-pre-wrap p-4 text-gray-200">{textPreview}</pre>
      {:else if previewKind(selected) === 'jsx'}
        {#if jsxPreviewMode === 'code'}
          <pre class="text-xs font-mono whitespace-pre-wrap p-4 text-gray-200">{textPreview}</pre>
        {:else if textPreview}
          <iframe title="JSX preview — {selected.title}" srcdoc={jsxPreviewSrcdoc(textPreview)} sandbox="allow-scripts" class="w-full h-full border-0 bg-white"></iframe>
        {:else}
          <div class="p-4 text-xs font-mono text-gray-500">loading JSX preview…</div>
        {/if}
      {:else if previewKind(selected) === 'html'}
        <iframe title={selected.title} src={selectedURL} sandbox="" class="w-full h-full border-0 bg-[#0f1117]"></iframe>
      {:else if previewKind(selected) === 'iframe'}
        <iframe title={selected.title} src={selectedURL} sandbox="" class="w-full h-full border-0 bg-[#0f1117]"></iframe>
      {:else}
        <div class="p-4 text-sm text-gray-400">No inline preview. <a class="text-cyan-300 underline" href={selectedURL} target="_blank" rel="noreferrer">Open artifact</a></div>
      {/if}
    </div>
  {/if}

  {#if textModalOpen || renameModalOpen}
    <div class="absolute inset-0 z-30 bg-black/70 flex items-center justify-center p-6" role="dialog" aria-modal="true" tabindex="-1" on:keydown={modalKeydown}>
      {#if textModalOpen}
        <form class="w-full max-w-xl bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-2xl overflow-hidden" on:submit|preventDefault={submitTextModal}>
          <div class="flex items-center justify-between border-b border-[#1e2d4a] px-4 py-3"><div class="text-sm font-mono text-cyan-300">New text artifact</div><button type="button" class="text-xs font-mono text-gray-500 hover:text-cyan-300" on:click={cancelTextModal}>[×]</button></div>
          <div class="p-4 space-y-3">
            <label class="block text-xs font-mono text-gray-400">Title <span class="text-gray-600">optional</span><input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={textModalTitle} placeholder={defaultTitle()} /></label>
            <label class="block text-xs font-mono text-gray-400">Text<textarea class="mt-1 w-full h-56 bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500 font-mono" bind:value={textModalText} placeholder="Paste text here…" autofocus on:keydown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); submitTextModal() } }}></textarea></label>
            <div class="grid grid-cols-3 gap-3"><label class="block text-xs font-mono text-gray-400">Format<select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={textModalFormat}><option value="md">Markdown</option><option value="jsx">JSX</option><option value="tsx">TSX</option><option value="txt">Plain text</option><option value="html">HTML</option><option value="log">Log</option></select></label><label class="block text-xs font-mono text-gray-400">Retention<select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={textModalRetention}><option value="session">session</option><option value="archive">archive</option></select></label><label class="block text-xs font-mono text-gray-400">Tags<input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={textModalTags} placeholder="design,notes" /></label></div>
            <div class="flex justify-between items-center pt-2"><div class="text-[11px] text-gray-500 font-mono">Esc cancels · ⌘/Ctrl+Enter creates · Shift+Enter inserts newline</div><div class="flex gap-2"><button type="button" class="px-3 py-1.5 text-xs font-mono text-gray-400 hover:text-cyan-300" on:click={cancelTextModal}>Cancel</button><button type="submit" class="px-3 py-1.5 text-xs font-mono text-cyan-200 bg-cyan-950/50 border border-cyan-900 rounded hover:border-cyan-500">Create</button></div></div>
          </div>
        </form>
      {:else}
        <form class="w-full max-w-lg bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-2xl overflow-hidden" on:submit|preventDefault={submitRenameModal}>
          <div class="flex items-center justify-between border-b border-[#1e2d4a] px-4 py-3"><div class="text-sm font-mono text-cyan-300">Edit artifact</div><button type="button" class="text-xs font-mono text-gray-500 hover:text-cyan-300" on:click={() => renameModalOpen = false}>[×]</button></div>
          <div class="p-4 space-y-3"><label class="block text-xs font-mono text-gray-400">Title<input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={renameTitle} autofocus /></label><label class="block text-xs font-mono text-gray-400">Summary<input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={renameSummary} /></label><div class="grid grid-cols-2 gap-3"><label class="block text-xs font-mono text-gray-400">Tags<input class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-3 py-2 text-sm text-gray-100 outline-none focus:border-cyan-500" bind:value={renameTags} /></label><label class="block text-xs font-mono text-gray-400">Retention<select class="mt-1 w-full bg-black/40 border border-[#1e2d4a] rounded px-2 py-2 text-sm text-gray-100" bind:value={renameRetention}><option value="session">session</option><option value="archive">archive</option></select></label></div><div class="flex justify-end gap-2 pt-2"><button type="button" class="px-3 py-1.5 text-xs font-mono text-gray-400 hover:text-cyan-300" on:click={() => renameModalOpen = false}>Cancel</button><button type="submit" class="px-3 py-1.5 text-xs font-mono text-cyan-200 bg-cyan-950/50 border border-cyan-900 rounded hover:border-cyan-500">Save</button></div></div>
        </form>
      {/if}
    </div>
  {/if}
</div>
