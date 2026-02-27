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

<div class="min-h-screen flex items-center justify-center bg-gray-950 p-4">
  <div class="w-full max-w-sm bg-gray-900 rounded-2xl p-8 shadow-xl">
    <h1 class="text-2xl font-bold text-white mb-2">devx</h1>
    <p class="text-gray-400 mb-6 text-sm">Enter your access token</p>
    <form on:submit|preventDefault={handleSubmit}>
      <input
        type="password"
        bind:value={token}
        placeholder="Token"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-4 text-base focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
      {#if error}<p class="text-red-400 text-sm mb-3">{error}</p>{/if}
      <button
        type="submit"
        disabled={loading}
        class="w-full bg-blue-600 hover:bg-blue-500 text-white font-semibold py-3 rounded-lg transition-colors"
      >
        {loading ? 'Signing in...' : 'Sign in'}
      </button>
    </form>
  </div>
</div>
