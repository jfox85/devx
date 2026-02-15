# Inline Search & Slot Bootstrap Design

Date: 2026-02-15

## Problem

Two UX issues with the TUI session manager:

1. **MRU slot numbers never appear** — Slots are only assigned on attach, so if you open the TUI without having attached since the feature was added, no numbers show. There's no bootstrapping.

2. **Search view is jarring** — Pressing `/` switches to a completely separate `stateSearch` / `searchView()` that drops the preview pane, loses visual context, and feels disconnected from the main list.

## Design

### Feature 1: Bootstrap MRU Slots on TUI Startup

In the `sessionsLoadedMsg` handler (`tui/model.go` ~line 837), after loading and reconciling slots, iterate through all sessions and call `AssignSlot` for any that don't already have one.

- `AssignSlot` already handles eviction (oldest `LastAttached` gets evicted when >9 sessions)
- First load: all sessions get slots 1-9
- Subsequent refreshes: no-op since sessions already have slots
- New sessions created during TUI: get a slot on next refresh (~2s)

No changes needed to `session/metadata.go`.

### Feature 2: Inline Search in Main List View

Replace the separate search state/view with inline filtering that stays in the main list view.

**Model changes:**
- Add `filterActive bool` field to track whether filter bar is shown
- Reuse existing `searchInput`, `searchFilter`, `filteredIndices`, `searchCursor`
- Remove `stateSearch` state constant

**Key handling (in `stateList`):**
- `/` → set `filterActive = true`, focus searchInput, Blink
- When `filterActive`: text input → searchInput, Esc clears filter, Enter closes filter and sets cursor to selected session
- Up/Down navigate only `filteredIndices` when filter active
- Number keys still work during filtering

**Rendering (`listView`):**
- When `filterActive && searchFilter != ""`: skip non-matching sessions, hide empty project groups
- Cursor uses `searchCursor` mapped through `filteredIndices`
- Search input box rendered at bottom of list (both preview and non-preview layouts)
- Footer shows search-mode keybindings when filter active

**Deletions:**
- `stateSearch` constant
- `searchView()` function
- `case stateSearch:` in Update and View
