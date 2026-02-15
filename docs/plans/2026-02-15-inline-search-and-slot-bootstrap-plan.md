# Inline Search & Slot Bootstrap Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix two TUI UX issues: bootstrap MRU slot numbers on startup so they always show, and replace the jarring separate search view with inline filtering in the main list.

**Architecture:** Both changes are in `tui/model.go`. Feature 1 adds a loop in the `sessionsLoadedMsg` handler. Feature 2 replaces `stateSearch` with a `filterActive` bool on `stateList`, merges search key handling into the list handler, and adds filter-awareness to the existing `listView()` render function.

**Tech Stack:** Go, Bubble Tea (charmbracelet/bubbletea), lipgloss

---

### Task 1: Bootstrap MRU Slots on Session Load

**Files:**
- Modify: `tui/model.go:837-845` (sessionsLoadedMsg handler)

**Step 1: Add slot bootstrapping after reconciliation**

In the `sessionsLoadedMsg` handler, after reconciling slots and before setting `m.numberedSlots`, loop through all sessions in the store and assign slots to any that don't have one:

```go
// Load numbered slots
slotStore, slotErr := session.LoadSessions()
if slotErr == nil {
    slotStore.ReconcileSlots()
    // Bootstrap: assign slots to any session that doesn't have one yet
    for name := range slotStore.Sessions {
        if slotStore.GetSlotForSession(name) == 0 {
            slotStore.AssignSlot(name)
        }
    }
    _ = slotStore.Save()
    m.numberedSlots = slotStore.NumberedSlots
} else {
    m.numberedSlots = make(map[int]string)
}
```

Note: `AssignSlot` already calls `Save()` internally, but we keep the outer `Save()` for the reconcile. The extra saves are harmless (idempotent file write).

**Step 2: Build and verify**

Run: `cd /Users/jfox/projects/devx/.worktrees/jf-faster-jumping && go build ./...`
Expected: Clean build, no errors.

**Step 3: Run tests**

Run: `go test -race ./...`
Expected: All tests pass.

**Step 4: Commit**

```bash
git add tui/model.go
git commit -m "feat: bootstrap MRU slots for all sessions on TUI load"
```

---

### Task 2: Add `filterActive` Field and Remove `stateSearch`

**Files:**
- Modify: `tui/model.go:52-63` (state constants)
- Modify: `tui/model.go:65-119` (model struct)

**Step 1: Remove `stateSearch` from the state enum**

In the `const` block at line 54-63, delete the `stateSearch` line:

```go
const (
    stateList state = iota
    stateCreating
    stateProjectSelect
    stateConfirm
    stateHostnames
    stateProjectManagement
    stateProjectAdd
)
```

**Step 2: Add `filterActive` field to model struct**

In the model struct, replace the comment above search fields (line 114) and add `filterActive`:

```go
// Search/filter fields
filterActive    bool
searchInput     textinput.Model
searchFilter    string
filteredIndices []int // indices into m.sessions that match the filter
searchCursor    int   // cursor within filtered results
```

**Step 3: Build to check for compile errors**

Run: `go build ./...`
Expected: Compile errors referencing `stateSearch` — this is expected, we'll fix them in the next tasks.

**Step 4: Commit (WIP)**

```bash
git add tui/model.go
git commit -m "refactor: replace stateSearch with filterActive field"
```

---

### Task 3: Merge Search Key Handling Into stateList

**Files:**
- Modify: `tui/model.go:482-572` (stateList key handler)
- Modify: `tui/model.go:775-816` (stateSearch key handler — delete)

**Step 1: Update the `/` key handler to set filterActive instead of switching state**

Replace the `case key.Matches(msg, m.keys.Search):` block at line 488-495:

```go
case key.Matches(msg, m.keys.Search):
    m.filterActive = true
    m.searchInput.Reset()
    m.searchInput.Focus()
    m.searchFilter = ""
    m.filteredIndices = nil
    m.searchCursor = 0
    return m, textinput.Blink
```

**Step 2: Add filter-active key handling at the TOP of the stateList case**

Right after `case stateList:` (line 483) and before the existing `switch`, add a guard that intercepts keys when filter is active:

```go
case stateList:
    // When filter is active, intercept keys for the search input
    if m.filterActive {
        switch {
        case key.Matches(msg, m.keys.Back): // Esc
            m.filterActive = false
            m.searchInput.Blur()
            m.searchFilter = ""
            m.filteredIndices = nil
            m.searchCursor = 0

        case msg.Type == tea.KeyEnter:
            // Jump cursor to selected filtered result, close filter
            if len(m.filteredIndices) > 0 && m.searchCursor < len(m.filteredIndices) {
                m.cursor = m.filteredIndices[m.searchCursor]
            }
            m.filterActive = false
            m.searchInput.Blur()
            m.searchFilter = ""
            m.filteredIndices = nil

        case key.Matches(msg, m.keys.Up):
            if m.searchCursor > 0 {
                m.searchCursor--
            }

        case key.Matches(msg, m.keys.Down):
            if len(m.filteredIndices) > 0 && m.searchCursor < len(m.filteredIndices)-1 {
                m.searchCursor++
            }

        default:
            var cmd tea.Cmd
            m.searchInput, cmd = m.searchInput.Update(msg)
            m.searchFilter = strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
            m.filteredIndices = nil
            m.searchCursor = 0
            for i, sess := range m.sessions {
                if m.searchFilter == "" || strings.Contains(strings.ToLower(sess.name), m.searchFilter) {
                    m.filteredIndices = append(m.filteredIndices, i)
                }
            }
            return m, cmd
        }
        return m, nil
    }

    switch {
    // ... existing stateList key handlers unchanged ...
```

**Step 3: Delete the entire `case stateSearch:` block**

Remove lines 775-816 (the `case stateSearch:` handler).

**Step 4: Build and verify**

Run: `go build ./...`
Expected: May still have compile errors from View function — that's ok, fixed in next task.

**Step 5: Commit**

```bash
git add tui/model.go
git commit -m "refactor: merge search key handling into stateList with filterActive"
```

---

### Task 4: Update View to Use Inline Filter

**Files:**
- Modify: `tui/model.go:1020-1062` (View function — state switch and footer)
- Modify: `tui/model.go:1096-1174` (listView non-preview rendering)
- Modify: `tui/model.go:1200-1248` (listView preview rendering)
- Delete: `tui/model.go:1809-1884` (searchView function)

**Step 1: Remove `stateSearch` from View's state switch**

In the View function at line 1035-1036, delete:
```go
case stateSearch:
    content = m.searchView()
```

**Step 2: Update footer to handle filterActive within stateList**

Replace the `case stateList:` footer (line 1045-1046) with:

```go
case stateList:
    if m.filterActive {
        footer = footerStyle.Width(m.width).Render("↑/↓: navigate • enter: jump to session • esc: cancel search")
    } else {
        footer = footerStyle.Width(m.width).Render("↑/↓: navigate • 1-9: jump (MRU) • /: search • enter: attach • c: create • d: delete • o: open routes • e: edit • h: hostnames • P: projects • p: preview • ?: help • q: quit")
    }
```

Also delete the `case stateSearch:` footer line (line 1059-1060).

**Step 3: Update non-preview listView to be filter-aware**

In the non-preview loop (lines 1117-1172), replace the session iteration with filter-aware logic. The key idea: when `filterActive` and a filter is typed, use `filteredIndices` to decide which sessions to show, and use `searchCursor` for the cursor highlight.

Replace the loop body (lines 1115-1172) with:

```go
// Determine which sessions to display
type displayEntry struct {
    sessIdx   int // index into m.sessions
    filterIdx int // position in filtered view (-1 if not filtered)
}

var entries []displayEntry
if m.filterActive && m.searchFilter != "" && m.filteredIndices != nil {
    for fi, si := range m.filteredIndices {
        entries = append(entries, displayEntry{sessIdx: si, filterIdx: fi})
    }
} else {
    for i := range m.sessions {
        entries = append(entries, displayEntry{sessIdx: i, filterIdx: -1})
    }
}

// Group sessions by project for display
var currentProject string
for _, entry := range entries {
    sess := m.sessions[entry.sessIdx]

    // Add project header if this is a new project
    if sess.projectAlias != currentProject {
        if currentProject != "" {
            b.WriteString("\n")
        }
        currentProject = sess.projectAlias

        projectHeader := "No Project"
        if sess.projectAlias != "" {
            if sess.projectName != "" {
                projectHeader = fmt.Sprintf("%s (%s)", sess.projectName, sess.projectAlias)
            } else {
                projectHeader = sess.projectAlias
            }
        }

        b.WriteString(headerStyle.Render(projectHeader) + "\n")
    }

    // Cursor logic: use searchCursor when filtering, else main cursor
    cursor := "  "
    if entry.filterIdx >= 0 {
        if entry.filterIdx == m.searchCursor {
            cursor = "> "
        }
    } else if m.cursor == entry.sessIdx {
        cursor = "> "
    }

    // Add MRU slot number if this session has one
    numberPrefix := "   "
    for slot, name := range m.numberedSlots {
        if name == sess.name {
            numberPrefix = fmt.Sprintf("%d. ", slot)
            break
        }
    }

    // Add attention indicator
    indicator := " "
    if sess.attentionFlag {
        indicator = "🔔"
    }

    isCursorLine := (entry.filterIdx >= 0 && entry.filterIdx == m.searchCursor) || (entry.filterIdx < 0 && m.cursor == entry.sessIdx)
    line := fmt.Sprintf("%s%s%s %s", cursor, numberPrefix, indicator, sess.name)
    if isCursorLine {
        line = selectedStyle.Render(line)
    }
    b.WriteString(line + "\n")

    // Show details for selected session (inline, only in non-filter mode)
    if !m.filterActive && m.cursor == entry.sessIdx {
        details := m.getSessionDetails(sess)
        lines := strings.Split(strings.TrimSuffix(details, "\n"), "\n")
        for _, line := range lines {
            b.WriteString(dimStyle.Render(line) + "\n")
        }
    }
}

// Show search input at bottom when filter is active
if m.filterActive {
    b.WriteString("\n")
    searchBox := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("170")).
        Padding(0, 1).
        Width(40).
        MarginLeft(2)
    b.WriteString(searchBox.Render("/ " + m.searchInput.View()))
}
```

**Step 4: Update preview listView with the same filter logic**

Apply the same `displayEntry` pattern to the preview layout loop (lines 1201-1248). Same logic but without the inline session details (preview layout doesn't show them). Add the search box at the bottom of the session list string, before the pane join.

**Step 5: Delete the `searchView()` function**

Remove the entire `searchView()` function (lines 1809-1884).

**Step 6: Build and verify**

Run: `go build ./...`
Expected: Clean build.

**Step 7: Run all tests**

Run: `go test -race ./...`
Expected: All pass.

**Step 8: Commit**

```bash
git add tui/model.go
git commit -m "feat: inline search filtering in main list view, remove separate search view"
```

---

### Task 5: Final Verification

**Files:** None (testing only)

**Step 1: Run full pre-commit checklist**

```bash
gofmt -w .
go vet ./...
golangci-lint run --timeout=5m
go test -v -race ./...
go mod tidy
```

Expected: All pass, no changes from gofmt.

**Step 2: Build and smoke test**

```bash
make dev
```

Launch the TUI and verify:
1. MRU slot numbers (1-9) appear next to sessions immediately on load
2. Pressing `/` shows a search box at the bottom of the main view (not a separate screen)
3. Typing filters the session list, keeping project group headers for matching sessions
4. Up/Down navigates filtered results
5. Enter closes the filter and leaves cursor on the selected session
6. Esc cancels the filter and restores the full list
7. Number keys (1-9) still work for slot jumping
8. Preview pane stays visible during search

**Step 3: Amend or create final commit if needed**

If gofmt or vet caught anything, fix and commit.
