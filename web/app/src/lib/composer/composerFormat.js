// web/app/src/lib/composer/composerFormat.js
//
// Small, DOM-free formatting helpers for the prompt history sheet. Kept out
// of composerStorage.js (pure persistence engine) and out of the Svelte
// component (so it's unit-testable with plain `node --test`), matching the
// usageFormat.js precedent in this codebase.

// relativeHistoryTime renders a compact "time since" label for a history
// entry's `at` timestamp (ms epoch): "just now", "4m ago", "2h ago", "3d ago".
// Falls back to "just now" for missing/invalid input so the UI never shows
// "NaN ago".
export function relativeHistoryTime(at, now = Date.now()) {
  const ms = Math.max(0, now - at)
  if (!Number.isFinite(at)) return 'just now'
  const minutes = Math.floor(ms / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(ms / 3600000)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(ms / 86400000)
  return `${days}d ago`
}
