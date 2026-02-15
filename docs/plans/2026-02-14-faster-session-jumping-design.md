# Faster Session Jumping

## Problem

The TUI currently assigns hotkeys 1-9 based on positional index in the project-grouped session list. With many sessions across multiple projects, sessions beyond the 9th have no quick-jump shortcut. Users bounce between 3-5 active sessions and need faster navigation.

## Design

Two complementary features: MRU-based slot-pinned numbering and fuzzy search.

### MRU Numbers with Slot Pinning

**Data model changes** (`session/metadata.go`):
- Add `LastAttached time.Time` to `SessionData` — updated every time you attach
- Add `NumberedSlots map[int]string` to `SessionStore` — persisted map of slot (1-9) to session name

**Slot assignment logic** (on attach):
1. If session already has a slot, do nothing (stable).
2. If there's a free slot (< 9 assigned), assign the lowest available number.
3. If all 9 slots are full, evict the session with the oldest `LastAttached` and give its slot to the new session.

**Startup reconciliation**: On load, remove any slots pointing to sessions that no longer exist. This frees up slots naturally when sessions are deleted.

**Display**: Number labels in the TUI come from `NumberedSlots` rather than positional index. Sessions not in a slot show no number. Numbers appear next to sessions wherever they fall in the project-grouped list.

**Key behavior**: Pressing `1`-`9` jumps to the session assigned to that slot, not the nth item in the list.

### Fuzzy Search

**Trigger**: Press `/` in the session list to enter search mode.

**Behavior**:
- A text input appears at the bottom of the session list.
- As you type, the session list filters to matching sessions (case-insensitive substring match on session name).
- Arrow keys navigate within filtered results.
- `Enter` jumps to the highlighted session and exits search mode.
- `Esc` cancels search and restores the full list.

**Implementation**:
- New `stateSearch` added to the state enum.
- Reuse the existing `textinput.Model` on the model.
- Filter is applied in the view — `m.sessions` stays unmodified, only rendering changes.
- Cursor resets to 0 within filtered results.

No persistence needed — search is purely ephemeral UI.

### Key Binding Changes

- `/` — enter search mode (only in `stateList`)
- `1`-`9` — jump to session by slot assignment rather than positional index
- Footer updated: `1-9: jump (MRU) • /: search`
