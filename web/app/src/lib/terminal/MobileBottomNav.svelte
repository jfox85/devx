<!-- web/app/src/lib/terminal/MobileBottomNav.svelte -->
<!--
  Operator-console mobile bottom navigation: Sessions · Terminal · Status ·
  Artifacts. Replaces the header back button as the primary mobile navigation
  so each surface is one thumb-reach tap. Rendered only below lg.
-->
<script>
  export let active = 'terminal'   // 'terminal' | 'status' | 'artifacts'
  export let attention = false     // show a notice dot on Status
  export let onSessions = () => {}
  export let onTerminal = () => {}
  export let onStatus = () => {}
  export let onArtifacts = () => {}

  const tabs = [
    { id: 'sessions', label: 'Sessions', icon: '▦' },
    { id: 'terminal', label: 'Terminal', icon: '❯_' },
    { id: 'status', label: 'Status', icon: '〰' },
    { id: 'artifacts', label: 'Artifacts', icon: '◆' },
  ]

  function handle(id) {
    if (id === 'sessions') onSessions()
    else if (id === 'terminal') onTerminal()
    else if (id === 'status') onStatus()
    else onArtifacts()
  }
</script>

<nav class="lg:hidden flex items-stretch bg-[#0a0e1a] border-t border-[#1e2d4a] shrink-0 pb-[env(safe-area-inset-bottom)]" aria-label="Session navigation">
  {#each tabs as tab (tab.id)}
    <button
      on:click={() => handle(tab.id)}
      aria-current={active === tab.id ? 'page' : undefined}
      class="relative flex-1 flex flex-col items-center justify-center gap-0.5 min-h-12 font-mono text-[10px] transition-colors
        {active === tab.id ? 'text-cyan-300 bg-cyan-950/30 border-t-2 border-cyan-500 -mt-px' : 'text-gray-600 hover:text-gray-300 border-t-2 border-transparent -mt-px'}"
    >
      {#if tab.id === 'status' && attention}
        <span class="absolute top-1.5 right-[calc(50%-18px)] w-1.5 h-1.5 rounded-full bg-orange-400" aria-hidden="true"></span>
      {/if}
      <span aria-hidden="true" class="text-sm leading-none">{tab.icon}</span>
      <span>{tab.label}</span>
    </button>
  {/each}
</nav>
