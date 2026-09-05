<script>
  export let open = false
  export let artifactsIsVisible = false
  export let splitMode = 'vertical'
  export let onToggle = () => {}
  export let onView = () => {}
  export let onAttachImage = () => {}
  export let onNewArtifact = () => {}
  export let onInsertArtifact = () => {}
  export let onToggleArtifacts = () => {}
  export let onCycleSplit = () => {}
  export let usageEnabled = false
  export let onShowUsage = () => {}
  export let onStatus = () => {}
  // Notification state: attention_flag → orange dot (status), unseen artifacts → cyan dot (artifacts).
  export let attention = false
  export let unseenArtifacts = 0
</script>

<div class="lg:hidden relative shrink-0 border-l border-[#1e2d4a]" data-actions-menu>
  <button
    on:click={onToggle}
    title="terminal and artifact actions"
    class="relative h-full px-3 text-gray-400 hover:text-cyan-300 text-sm font-mono flex items-center transition-colors"
    aria-expanded={open}
    aria-haspopup="menu"
  >[☰]{#if attention || unseenArtifacts > 0}<span class="absolute top-1.5 right-1.5 w-1.5 h-1.5 rounded-full {attention ? 'bg-orange-400' : 'bg-cyan-400'}" aria-hidden="true"></span>{/if}</button>
  {#if open}
    <div class="absolute right-1 top-9 z-[70] w-64 bg-[#0b1020] border border-[#1e2d4a] rounded-lg shadow-2xl overflow-hidden" role="menu">
      <div class="px-3 py-2 text-[10px] uppercase tracking-wide font-mono text-cyan-500 border-b border-[#1e2d4a]">terminal</div>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e]" role="menuitem" on:click={onView}>View terminal output</button>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e] flex items-center gap-2" role="menuitem" on:click={onStatus}>
        <span>Session status &amp; routes</span>
        {#if attention}<span class="w-1.5 h-1.5 rounded-full bg-orange-400 shrink-0" aria-hidden="true"></span><span class="sr-only">needs attention</span>{/if}
      </button>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e]" role="menuitem" on:click={onAttachImage}>Attach image</button>
      <div class="px-3 py-2 text-[10px] uppercase tracking-wide font-mono text-cyan-500 border-b border-[#1e2d4a]">artifacts</div>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e]" role="menuitem" on:click={onNewArtifact}>New artifact</button>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e]" role="menuitem" on:click={onInsertArtifact}>Insert artifact reference</button>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 border-b border-[#111a2e] flex items-center gap-2" role="menuitem" on:click={onToggleArtifacts}>
        <span>{artifactsIsVisible ? 'Hide artifacts panel' : 'Show artifacts panel'}</span>
        {#if unseenArtifacts > 0}<span class="text-[10px] font-mono text-cyan-300 bg-cyan-950/60 border border-cyan-800 rounded-full px-1.5 shrink-0">{unseenArtifacts} new</span>{/if}
      </button>
      <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30 {usageEnabled ? 'border-b border-[#111a2e]' : ''}" role="menuitem" on:click={onCycleSplit}>Change split: {splitMode}</button>
      {#if usageEnabled}
        <div class="px-3 py-2 text-[10px] uppercase tracking-wide font-mono text-cyan-500 border-b border-[#1e2d4a]">usage</div>
        <button class="w-full text-left px-4 py-3 text-sm font-mono text-gray-200 hover:bg-cyan-950/30" role="menuitem" on:click={onShowUsage}>Provider usage</button>
      {/if}
    </div>
  {/if}
</div>
