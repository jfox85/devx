# Composer Draft Persistence and Prompt History

Status: proposal — not yet implemented

## Goal

Stop losing typed prompt text in the composer, especially on mobile where retyping is most expensive.

Two concrete losses to fix:

1. **Submitted but didn't land.** The prompt was sent, but a bad connection or a terminal in the wrong state meant it never took effect. The text is gone from the box and unrecoverable.
2. **Typed, then switched away.** A prompt is composed, the user switches tabs/apps/sessions to wait, and the mobile browser discards the page. Returning shows an empty composer.

## Why it happens today

`web/app/src/lib/stores/sessionUiState.js:1-6` is explicit: composer drafts live in a module-level `Map` and are **memory-only by design** — *"so prompt text doesn't outlive the tab."* Per-session switching works (`PromptComposer.svelte:28-35` saves the outgoing session's draft and loads the incoming one), but the moment the page context is torn down, everything is gone. On mobile that happens routinely: iOS/Android reclaim backgrounded tabs aggressively, so "switch to another app for two minutes" is enough.

For case 1, `send()` (`PromptComposer.svelte:70-85`) already does the right thing on a *thrown* error: `text` is only cleared after `await sendInput(...)` resolves, so a network failure leaves the text in place and shows `error`. The gap is the silent failures — HTTP 200 but the keystrokes didn't do what the user intended (wrong tmux mode, agent not at a prompt, pane busy) — plus any send whose tab is discarded mid-flight. Those need a durable record of what was sent, not just retry-on-throw.

This is an anticipated evolution, not a reversal: the original plan (`docs/plans/2026-06-11-devx-desktop-control-deck.md:361`) says drafts are *"memory-only by default **unless explicitly enabled later**"*, and its acceptance criterion is *"Sensitive prompt history is not persisted **by default**"* (`:367`).

## Product decisions

### Persist the pending draft per session, always-on

A draft is text the user typed and did not send. Losing it is never useful. Persist it to `localStorage` keyed by session, restore on mount, and clear it on successful send.

Scoped per session (matching today's in-memory behavior) so switching sessions doesn't cross-contaminate, and so a draft returns to the session it was written for.

### Keep a short prompt history per session, on by default

Every explicit send appends to a per-session history ring (newest first, **20** entries, deduped against the immediately previous entry). This is what makes case 1 recoverable: the send may have gone nowhere useful, but the text is still one tap away.

History records **sends the user initiated** — both `submit: true` (↵) and `submit: false` (¶ paste-only) — because both represent "text I committed and no longer have in the box."

### Recall UI: a history button next to the composer

Mobile is where retyping hurts, so v1 ships the docked composer only; the desktop overlay gets the same sheet as a fast-follow once the interaction has been used in anger.

- **Docked (mobile), v1:** a `⏱` button left of `¶`, **always visible** (not gated on `history.length > 0`) so the privacy controls in the sheet's footer (below) stay reachable even when history is empty or disabled. Opens a bottom sheet listing recent prompts (newest first) with relative timestamps; with no history, the sheet still opens showing an empty state plus the prefs footer.
- **Overlay (desktop), deferred:** a `history` button in the overlay footer opening the same component. Remains absent from the desktop overlay in v1, per the original decision.
- **iOS keyboard dismissal:** opening the sheet explicitly blurs the composer textarea *before* flipping the sheet open, so the on-screen keyboard collapses first instead of fighting the sheet's entrance for viewport space.
- **Safe-area:** the sheet's inner container carries `pb-[env(safe-area-inset-bottom)]` so its content (including the prefs footer) isn't obscured by a device's home indicator/notch.
- **Focus:** the history trigger button is bound and refocused after an ordinary close; a recall continues to focus the textarea, as before.

Draft persistence itself is variant-independent, so desktop still gets the problem-2 fix in v1.

Tapping an entry **loads it into the composer** (replacing current text only if the box is empty; otherwise appending on a new line, so recall never destroys what you're typing). It does not auto-send — the user stays in control, which matters when the reason it's in history is that sending misbehaved.

The sheet also offers **clear history** for this session, gated behind a two-step confirmation: the first tap arms the control (label flips to a danger-toned `[confirm clear?]`) without clearing anything; a second tap within 5 seconds performs the clear. The confirmation disarms automatically after 5 seconds, on Escape, and on the sheet closing/unmounting, so a stray later tap can never clear history. Clearing keeps the sheet open, showing the empty state — it is not treated as a close.

### Storage: `localStorage`, one key **per session**, capped and resilient

```text
devx_composer_v1:<session>  = { draft: "…", history: [ { text, at }, … ], sending: false }
devx_composer_v1__index     = { "<session>": <lastWriteMs>, … }     // LRU bookkeeping only
```

**One key per session, not one blob for everything.** A single key would require read-modify-write of the whole blob on every debounced save; with two tabs open on different sessions — the normal devx pattern, and exactly the "switch tabs/sessions" case this feature is for — the second writer would clobber the first tab's unrelated draft using its own stale read. Per-session keys make writes from different sessions independent and reduce the race to "same session open in two tabs," which is both rare and benign (last writer wins on text the user is editing in both places).

The small index key is written only on create/evict, not on every keystroke, so it is not a contention point.

- try/catch around every storage call, graceful fallback when storage is unavailable or private mode, following the `sessionOrdering.js` convention. (That file is precedent for the *error handling*, not for the key layout — it stores a single scalar with no contention profile.)
- **Caps:** 20 history entries per session, **2 KB per entry** (long prompts truncate with a marker), 40 sessions (LRU via the index), ~512 KB total. 20 × 2 KB × 40 ≈ 1.6 MB worst case against a typical 5 MB origin quota, so the 512 KB budget is a backstop rather than a routine path — eviction should be rare, which keeps the "last 20 sends" guarantee honest for any session in active use.
- **Quota failures are non-fatal.** A `QuotaExceededError` must never break sending or typing; degrade to memory-only for the rest of the page lifetime and log once.
- Writes are **debounced (400 ms)** while typing, plus flushed immediately on `visibilitychange → hidden` and `pagehide`. That covers the mobile discard case, which is precisely when a debounce timer would never fire.

### In-flight sends: warn rather than silently invite a duplicate

The draft is deliberately only cleared after `sendInput` resolves. If the tab is killed *during* that await — the exact mobile-discard scenario this feature targets — the send may already have reached tmux while the draft is still on disk. Restoring it silently would invite the user to send the same prompt twice.

So `send()` persists a `sending: true` flag alongside the draft on start and clears it in `finally`. If a draft is restored with that flag still set, the composer shows a subdued one-line hint — *"this may already have been sent — check the terminal before resending"* — which the user dismisses or ignores. Cheap, and it turns a silent duplicate into an informed choice.

### Privacy: client-only and on by default

The user explicitly chose default-on persistence after reviewing the privacy tradeoff. Both drafts and the last 20 sent prompts are stored only in this browser's `localStorage`: they never cross `/api/*`, never enter DevX metadata, and never sync to another device. This is low risk for this loopback/tailnet-only, token-authenticated development UI, and sent prompts already exist in tmux scrollback and Gatepost logs on the same machine.

This intentionally changes the older control-deck plan's default (*"Sensitive prompt history is not persisted by default"*) based on an explicit product decision. The escape hatch remains client-only (`devx_composer_prefs_v1`): `draft` and `history` both default on, turning either off **purges** its stored data, and the history sheet has a per-session **clear history** action. The server has no enforcement role and gains no endpoint or setting.

These preferences are reachable from the product UI itself, not only via `localStorage` devtools: the always-visible `⏱` button (see above) opens a bottom sheet whose `local storage` footer renders two compact toggles — `save drafts on this device` and `keep sent prompt history` — both default checked, each with a stable `aria-label` and the explanatory copy `saved only in this browser`. Turning history off purges it immediately and keeps the sheet open; turning it back on re-enables future recording. Turning drafts off purges the *persisted* draft only — text currently sitting in the textarea is untouched (memory-only for the rest of the page lifetime), matching the storage layer's existing memory-only-on-quota-failure behavior.

**Non-goal:** DevX assumes a single-user local browser profile. A generic `401`/token-rotation event does **not** purge drafts or history — doing so would turn a transient auth failure into silent data loss. Purging is a deliberate user action taken through the client controls described above (or clearing `localStorage` directly), never an automatic side effect of an auth error.

The desktop security invariant that matters (`docs/plans/2026-06-11-devx-desktop-control-deck.md:64-95`) forbids **tokens** in localStorage; prompt text is a different class of data, and the control-deck plan explicitly contemplated enabling draft persistence later.

### Sessions that go away

When a session is deleted, drop its entry. `SessionList` already knows the live session set; the composer store exposes `pruneSessions(names)` called after each session list load, so history for removed sessions doesn't linger indefinitely.

## Implementation plan

Pure logic lives in a plain `.js` module so it is unit-testable without mounting Svelte (the `sessionOrdering.js` precedent; vitest has no Svelte plugin here).

```
web/app/src/lib/composer/composerStorage.js        NEW — pure store: per-session keys, caps, eviction, prune, prefs, quota fallback
web/app/src/lib/composer/composerStorage.test.js   NEW — node --test (registered in package.json test:node)
web/app/src/lib/composer/PromptHistorySheet.svelte NEW — recall list (bottom sheet)
web/app/src/lib/composer/PromptComposer.svelte     draft restore/persist, history append on send, recall button, in-flight hint
web/app/src/lib/stores/sessionUiState.js           delegate draft get/set to composerStorage; update the stale comment
web/app/src/lib/SessionList.svelte                 call pruneSessions after a load
web/app/tests/composer.spec.js                     NEW — Playwright: reload persistence, recall, default-on, disabled mode
```

No Go, API, or `web/dist`-adjacent backend changes: this is a client-only feature.
```
```

### Behavior slices (TDD order)

1. `composerStorage` round-trips a draft per session; unavailable/broken storage degrades to memory without throwing.
2. Draft survives a page reload and restores into the composer (the case-2 fix).
3. Writes for two different sessions do not clobber each other (the per-session-key requirement, testable directly against the storage module).
4. Successful send clears the draft and appends to history when the client preference is enabled (on by default).
5. History is capped at 20, newest first, consecutive duplicates collapsed; disabling history purges it.
6. Recall inserts into an empty composer; appends on a new line when the composer has text.
7. Flush on `visibilitychange → hidden` / `pagehide` persists without waiting for the debounce.
8. Oversized entries truncate; quota failure degrades silently and keeps sending working.
9. A draft restored with `sending: true` shows the may-already-have-sent hint.
10. Disabling a pref purges its data and restores today's behavior.
11. `pruneSessions` drops entries for deleted sessions.

## UX states

| State | Docked (mobile), v1 | Overlay (desktop), v1 |
|---|---|---|
| History off (explicit preference) | `⏱` still visible; sheet opens empty with prefs footer | draft persistence only |
| History on (default), empty | `⏱` still visible; sheet opens empty with prefs footer | — |
| History on, non-empty | `⏱` left of `¶` | — (deferred) |
| Sheet open | bottom sheet, newest first, relative times, tap to recall, safe-area bottom padding | — |
| Recall into empty box | replaces text, focuses end | — |
| Recall into non-empty box | appends after a newline | — |
| Clear history | two-step: first tap arms `[confirm clear?]`, second clears; disarms after 5s / Escape / close | — |
| Sheet close (ordinary) | focus returns to the `⏱` trigger button | — |
| Draft restored mid-send | subdued "may already have been sent" hint | same |
| Prefs off | drafts memory-only, stored data purged | same |
| Storage unavailable | everything works, memory-only | same |

## Verification

- `node --test` for `composerStorage` (caps, eviction, truncation, quota, prune, dedupe).
- Playwright `composer.spec.js` at 390 px and 1280 px: type → reload → draft restored; send → history entry appears → recall restores it; recall into non-empty box appends; disabled mode stores nothing; `localStorage` blocked → composer still sends.
- Manual on the deployed instance over Tailscale from a real phone: type a prompt, background the browser, return, confirm the text is there; enable history, send a prompt, confirm it can be recalled.
- Two tabs on different sessions, both typing: confirm neither draft is lost (the multi-tab case that motivated per-session keys).

## Acceptance criteria

- A typed, unsent prompt survives a tab discard / reload / app switch and returns to the session it was written for.
- With history enabled, the last 20 sends per session are recoverable for any session in active use (subject to the total-budget backstop, which evicts least-recently-used sessions first and never evicts a draft).
- Drafts for different sessions in different tabs do not clobber each other.
- Recall never destroys text currently in the composer.
- A draft restored while a send was in flight warns before the user can silently duplicate it.
- Sending still works with `localStorage` unavailable or full.
- Draft/history stay client-only; disabling either default-on preference purges its stored data.
- No prompt text crosses `/api/*`; no new endpoint, no Go changes.

## Deferred

- Cross-device history sync (would require a server store and a real privacy decision).
- Editing/pinning history entries.
- Global (cross-session) history search in the quick switcher.
- Undo for "clear history".
