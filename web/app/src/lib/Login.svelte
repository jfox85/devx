<!-- web/app/src/lib/Login.svelte -->
<script>
  import { login } from '../api.js'
  let token = ''
  let error = ''
  let loading = false

  async function handleSubmit() {
    loading = true
    error = ''
    try {
      await login(token)
      window.location.reload()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
</script>

<div class="h-dvh flex items-center justify-center bg-[#0a0e1a] p-4">
  <div class="w-full max-w-xs">
    <!-- TUI-style box border -->
    <div class="border border-[#1e2d4a] p-8">
      <div class="text-cyan-400 font-mono font-bold text-lg tracking-widest mb-1">devx</div>
      <div class="text-gray-600 text-xs font-mono mb-6 tracking-wide">enter access token</div>
      <form on:submit|preventDefault={handleSubmit}>
        <input
          type="password"
          bind:value={token}
          placeholder="token"
          autofocus
          class="
            w-full bg-transparent border border-[#1e2d4a] focus:border-cyan-800
            text-gray-300 text-xs font-mono px-3 py-2 mb-4
            outline-none transition-colors placeholder-gray-700
          "
        />
        {#if error}
          <p class="text-red-500 text-xs font-mono mb-3">{error}</p>
        {/if}
        <button
          type="submit"
          disabled={loading}
          class="
            w-full border border-cyan-900 hover:border-cyan-700
            text-cyan-500 hover:text-cyan-300
            text-xs font-mono py-2 transition-colors
            disabled:opacity-40
          "
        >
          {loading ? 'authenticating...' : '[ sign in ]'}
        </button>
      </form>
    </div>
  </div>
</div>
