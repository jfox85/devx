<script>
  export let session
  export let url = ''
  export let onClose = () => {}
</script>

<div class="fixed inset-0 z-[1000] bg-[#020617] border border-[#1e2d4a] shadow-2xl flex flex-col">
  <div class="h-9 shrink-0 flex items-center gap-2 border-b border-[#1e2d4a] px-3 bg-[#0a0e1a]">
    <div class="text-xs font-mono text-cyan-300 flex-1 truncate">terminal output · {session.name}</div>
    <a class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" href={url} target="_blank" rel="noreferrer">[open tab]</a>
    <button class="text-[11px] font-mono text-gray-500 hover:text-cyan-300" title="close terminal output viewer" on:click={onClose}>[close]</button>
  </div>
  <iframe title="Full terminal output — {session.name}" src={url} class="flex-1 min-h-0 w-full border-0 bg-[#020617]"></iframe>
</div>
