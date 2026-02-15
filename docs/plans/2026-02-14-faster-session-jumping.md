# Faster Session Jumping Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add MRU-based slot-pinned number keys (1-9) and fuzzy search (/) to the TUI for faster session navigation.

**Architecture:** Extend the persisted `SessionStore` with `LastAttached` timestamps and a `NumberedSlots` map. Slot assignment is automatic on attach, stable once assigned. Fuzzy search is ephemeral TUI state using a filtered index overlay.

**Tech Stack:** Go, Bubble Tea (TUI), Cobra (CLI), JSON persistence

---

### Task 1: Add `LastAttached` field to Session

**Files:**
- Modify: `session/metadata.go:15-29` (Session struct)
- Test: `session/metadata_test.go`

**Step 1: Write the failing test**

Add to `session/metadata_test.go`:

```go
func TestSessionLastAttached(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("test-sess", "main", "/path", map[string]int{"PORT": 3000})

	// LastAttached should be zero initially
	sess, _ := store.GetSession("test-sess")
	if !sess.LastAttached.IsZero() {
		t.Error("expected LastAttached to be zero initially")
	}

	// Record attach
	err = store.RecordAttach("test-sess")
	if err != nil {
		t.Fatalf("failed to record attach: %v", err)
	}

	sess, _ = store.GetSession("test-sess")
	if sess.LastAttached.IsZero() {
		t.Error("expected LastAttached to be set after RecordAttach")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestSessionLastAttached -v`
Expected: FAIL — `RecordAttach` not defined

**Step 3: Write minimal implementation**

In `session/metadata.go`, add `LastAttached` to the `Session` struct:

```go
type Session struct {
	Name            string            `json:"name"`
	ProjectAlias    string            `json:"project_alias,omitempty"`
	ProjectPath     string            `json:"project_path,omitempty"`
	Branch          string            `json:"branch"`
	Path            string            `json:"path"`
	Ports           map[string]int    `json:"ports"`
	Routes          map[string]string `json:"routes,omitempty"`
	EditorPID       int               `json:"editor_pid,omitempty"`
	AttentionFlag   bool              `json:"attention_flag,omitempty"`
	AttentionReason string            `json:"attention_reason,omitempty"`
	AttentionTime   time.Time         `json:"attention_time,omitempty"`
	LastAttached    time.Time         `json:"last_attached,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}
```

Add `RecordAttach` method to `SessionStore`:

```go
// RecordAttach updates the LastAttached timestamp for a session
func (s *SessionStore) RecordAttach(name string) error {
	return s.UpdateSession(name, func(sess *Session) {
		sess.LastAttached = time.Now()
	})
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./session/ -run TestSessionLastAttached -v`
Expected: PASS

**Step 5: Commit**

```bash
git add session/metadata.go session/metadata_test.go
git commit -m "feat: add LastAttached timestamp to Session"
```

---

### Task 2: Add `NumberedSlots` to SessionStore with slot logic

**Files:**
- Modify: `session/metadata.go:31-33` (SessionStore struct)
- Test: `session/metadata_test.go`

**Step 1: Write the failing tests**

Add to `session/metadata_test.go`:

```go
func TestNumberedSlots_AssignSlot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_ = store.AddSession("sess-b", "main", "/b", map[string]int{})

	// Assign slot for sess-a — should get slot 1 (lowest available)
	slot, err := store.AssignSlot("sess-a")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 1 {
		t.Errorf("expected slot 1, got %d", slot)
	}

	// Assign slot for sess-b — should get slot 2
	slot, err = store.AssignSlot("sess-b")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 2 {
		t.Errorf("expected slot 2, got %d", slot)
	}

	// Assign again for sess-a — should keep slot 1 (stable)
	slot, err = store.AssignSlot("sess-a")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 1 {
		t.Errorf("expected sess-a to keep slot 1, got %d", slot)
	}
}

func TestNumberedSlots_EvictLRU(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()

	// Create 10 sessions, assign slots to first 9
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("sess-%d", i)
		_ = store.AddSession(name, "main", fmt.Sprintf("/%d", i), map[string]int{})
		// Set LastAttached so sess-1 is oldest
		_ = store.UpdateSession(name, func(s *Session) {
			s.LastAttached = time.Now().Add(time.Duration(i) * time.Minute)
		})
	}

	for i := 1; i <= 9; i++ {
		_, _ = store.AssignSlot(fmt.Sprintf("sess-%d", i))
	}

	// All 9 slots full. Assign slot for sess-10 — should evict sess-1 (oldest LastAttached)
	slot, err := store.AssignSlot("sess-10")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}

	// sess-10 should have taken sess-1's slot (slot 1)
	if slot != 1 {
		t.Errorf("expected sess-10 to get slot 1 (evicting sess-1), got %d", slot)
	}

	// sess-1 should no longer have a slot
	if s := store.GetSlotForSession("sess-1"); s != 0 {
		t.Errorf("expected sess-1 to have no slot, got %d", s)
	}
}

func TestNumberedSlots_Reconcile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_, _ = store.AssignSlot("sess-a")

	// Remove session, then reconcile — slot should be freed
	_ = store.RemoveSession("sess-a")
	store.ReconcileSlots()

	if s := store.GetSlotForSession("sess-a"); s != 0 {
		t.Errorf("expected no slot for removed session, got %d", s)
	}
}

func TestNumberedSlots_GetSessionForSlot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_, _ = store.AssignSlot("sess-a")

	name := store.GetSessionForSlot(1)
	if name != "sess-a" {
		t.Errorf("expected 'sess-a' for slot 1, got '%s'", name)
	}

	name = store.GetSessionForSlot(5)
	if name != "" {
		t.Errorf("expected empty for unassigned slot 5, got '%s'", name)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./session/ -run "TestNumberedSlots" -v`
Expected: FAIL — methods not defined

**Step 3: Write implementation**

In `session/metadata.go`, update `SessionStore`:

```go
type SessionStore struct {
	Sessions      map[string]*Session `json:"sessions"`
	NumberedSlots map[int]string      `json:"numbered_slots,omitempty"`
}
```

Update `LoadSessions` to initialize `NumberedSlots` if nil (after the existing `Sessions` nil check):

```go
if store.NumberedSlots == nil {
	store.NumberedSlots = make(map[int]string)
}
```

Also update `ClearRegistry`:

```go
func ClearRegistry() error {
	store := &SessionStore{
		Sessions:      make(map[string]*Session),
		NumberedSlots: make(map[int]string),
	}
	return store.Save()
}
```

Add slot methods:

```go
// AssignSlot assigns a numbered slot (1-9) to a session.
// If the session already has a slot, returns the existing slot (stable).
// If a free slot exists, assigns the lowest available.
// If all 9 are full, evicts the session with the oldest LastAttached.
func (s *SessionStore) AssignSlot(name string) (int, error) {
	if _, exists := s.Sessions[name]; !exists {
		return 0, fmt.Errorf("session '%s' not found", name)
	}

	// Check if session already has a slot
	if slot := s.GetSlotForSession(name); slot != 0 {
		return slot, nil
	}

	// Find lowest available slot (1-9)
	for i := 1; i <= 9; i++ {
		if _, taken := s.NumberedSlots[i]; !taken {
			s.NumberedSlots[i] = name
			return i, s.Save()
		}
	}

	// All slots full — evict the session with the oldest LastAttached
	oldestSlot := 0
	var oldestTime time.Time
	for slot, sessName := range s.NumberedSlots {
		sess, exists := s.Sessions[sessName]
		if !exists {
			// Stale slot, use it immediately
			s.NumberedSlots[slot] = name
			return slot, s.Save()
		}
		if oldestSlot == 0 || sess.LastAttached.Before(oldestTime) {
			oldestSlot = slot
			oldestTime = sess.LastAttached
		}
	}

	s.NumberedSlots[oldestSlot] = name
	return oldestSlot, s.Save()
}

// GetSlotForSession returns the slot number for a session, or 0 if unassigned.
func (s *SessionStore) GetSlotForSession(name string) int {
	for slot, sessName := range s.NumberedSlots {
		if sessName == name {
			return slot
		}
	}
	return 0
}

// GetSessionForSlot returns the session name assigned to a slot, or "" if empty.
func (s *SessionStore) GetSessionForSlot(slot int) string {
	return s.NumberedSlots[slot]
}

// ReconcileSlots removes slot assignments for sessions that no longer exist.
func (s *SessionStore) ReconcileSlots() {
	for slot, name := range s.NumberedSlots {
		if _, exists := s.Sessions[name]; !exists {
			delete(s.NumberedSlots, slot)
		}
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./session/ -run "TestNumberedSlots" -v`
Expected: PASS

**Step 5: Run all session tests to check for regressions**

Run: `go test ./session/ -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add session/metadata.go session/metadata_test.go
git commit -m "feat: add NumberedSlots with slot assignment and eviction logic"
```

---

### Task 3: Record attach + assign slot on session attach

**Files:**
- Modify: `cmd/session_attach.go:24-73` (runSessionAttach)
- Modify: `tui/model.go:1743-1791` (attachSession method)

**Step 1: Update CLI attach command**

In `cmd/session_attach.go`, after the attention flag clearing block (line 53) and before the tmux attach (line 56), add:

```go
// Record attach time and assign a numbered slot
if err := store.RecordAttach(name); err != nil {
	fmt.Printf("Warning: Failed to record attach time: %v\n", err)
}
if _, err := store.AssignSlot(name); err != nil {
	fmt.Printf("Warning: Failed to assign slot: %v\n", err)
}
```

**Step 2: Update TUI attach**

In `tui/model.go`, inside the `attachSession` method, after the attention flag check/clear and before the `attachCmd` call (around line 1774), add the same logic:

```go
// Record attach time and assign a numbered slot
if err := store.RecordAttach(name); err != nil {
	m.debugLogger.Printf("Warning: Failed to record attach time: %v", err)
}
if _, err := store.AssignSlot(name); err != nil {
	m.debugLogger.Printf("Warning: Failed to assign slot: %v", err)
}
```

**Step 3: Run full test suite**

Run: `go test ./... -v`
Expected: All PASS (no test for this wiring — covered by integration)

**Step 4: Commit**

```bash
git add cmd/session_attach.go tui/model.go
git commit -m "feat: record LastAttached and assign slot on session attach"
```

---

### Task 4: Wire MRU slots into TUI display and key handling

**Files:**
- Modify: `tui/model.go`

**Step 1: Add `numberedSlots` to TUI model**

Add a field to the `model` struct (around line 64):

```go
numberedSlots map[int]string // slot number -> session name (loaded from store)
```

Initialize it in `InitialModel()`:

```go
numberedSlots: make(map[int]string),
```

**Step 2: Load slots when sessions load**

In the `sessionsLoadedMsg` handler (around line 747), after sessions are set, load and reconcile slots:

```go
// Load numbered slots
slotStore, slotErr := session.LoadSessions()
if slotErr == nil {
	slotStore.ReconcileSlots()
	_ = slotStore.Save()
	m.numberedSlots = slotStore.NumberedSlots
} else {
	m.numberedSlots = make(map[int]string)
}
```

**Step 3: Add helper to find session index by name**

```go
// sessionIndexByName returns the index of a session in m.sessions, or -1.
func (m *model) sessionIndexByName(name string) int {
	for i, s := range m.sessions {
		if s.name == name {
			return i
		}
	}
	return -1
}
```

**Step 4: Change number key handler**

Replace the existing number key handler in `stateList` (lines 537-542):

```go
// Handle number keys 1-9 for quick navigation (MRU slot-based)
case msg.String() >= "1" && msg.String() <= "9":
	slot := int(msg.String()[0] - '0')
	if sessName, ok := m.numberedSlots[slot]; ok {
		if idx := m.sessionIndexByName(sessName); idx >= 0 {
			m.cursor = idx
		}
	}
```

**Step 5: Change display to show slot numbers instead of positional numbers**

In `listView()`, replace the positional number prefix logic. This appears in two places (non-preview at ~line 1057, and preview at ~line 1141). In both places, replace:

```go
// Add number shortcut for first 9 items
numberPrefix := ""
if i < 9 {
	numberPrefix = fmt.Sprintf("%d. ", i+1)
} else {
	numberPrefix = "   " // Maintain alignment
}
```

With:

```go
// Add MRU slot number if this session has one
numberPrefix := "   " // Default: no number
for slot, name := range m.numberedSlots {
	if name == sess.name {
		numberPrefix = fmt.Sprintf("%d. ", slot)
		break
	}
}
```

**Step 6: Run the app manually to verify**

Run: `go build -o /tmp/devx-test . && /tmp/devx-test`
Verify: Sessions show slot numbers, pressing number keys jumps to the correct session.

**Step 7: Commit**

```bash
git add tui/model.go
git commit -m "feat: wire MRU slot numbers into TUI display and key handling"
```

---

### Task 5: Add fuzzy search mode

**Files:**
- Modify: `tui/model.go`

**Step 1: Add search state and fields**

Add `stateSearch` to the state enum (after `stateProjectAdd`):

```go
stateSearch
```

Add fields to `model` struct:

```go
searchInput     textinput.Model
searchFilter    string
filteredIndices []int // indices into m.sessions that match the filter
searchCursor    int   // cursor within filtered results
```

Initialize `searchInput` in `InitialModel()`:

```go
si := textinput.New()
si.Placeholder = "search sessions..."
si.CharLimit = 50
```

And in the model initialization:

```go
searchInput: si,
```

**Step 2: Add `/` key binding**

Add to `keyMap` struct:

```go
Search key.Binding
```

Add to `keys` var:

```go
Search: key.NewBinding(
	key.WithKeys("/"),
	key.WithHelp("/", "search"),
),
```

**Step 3: Add search key handler in `stateList`**

In the `stateList` switch block, add a case before the number keys handler:

```go
case key.Matches(msg, m.keys.Search):
	m.state = stateSearch
	m.searchInput.Reset()
	m.searchInput.Focus()
	m.searchFilter = ""
	m.filteredIndices = nil
	m.searchCursor = 0
	return m, textinput.Blink
```

**Step 4: Add `stateSearch` key handling in Update**

Add a new case in the `m.state` switch:

```go
case stateSearch:
	switch {
	case key.Matches(msg, m.keys.Back):
		m.state = stateList
		m.searchInput.Blur()
		m.searchFilter = ""
		m.filteredIndices = nil

	case msg.Type == tea.KeyEnter:
		// Jump to selected filtered result
		if len(m.filteredIndices) > 0 && m.searchCursor < len(m.filteredIndices) {
			m.cursor = m.filteredIndices[m.searchCursor]
		}
		m.state = stateList
		m.searchInput.Blur()
		m.searchFilter = ""
		m.filteredIndices = nil

	case key.Matches(msg, m.keys.Up):
		if m.searchCursor > 0 {
			m.searchCursor--
		}

	case key.Matches(msg, m.keys.Down):
		if m.searchCursor < len(m.filteredIndices)-1 {
			m.searchCursor++
		}

	default:
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		// Update filter
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
```

**Step 5: Add search view rendering**

In the `View()` method's state switch, add:

```go
case stateSearch:
	content = m.searchView()
```

Add the `searchView` method:

```go
func (m *model) searchView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Sessions") + "\n\n")

	if len(m.filteredIndices) == 0 && m.searchFilter != "" {
		b.WriteString(dimStyle.Render("  No matching sessions") + "\n")
	} else {
		indices := m.filteredIndices
		if indices == nil {
			// Show all sessions before any typing
			indices = make([]int, len(m.sessions))
			for i := range m.sessions {
				indices[i] = i
			}
		}

		var currentProject string
		for filterIdx, sessIdx := range indices {
			sess := m.sessions[sessIdx]

			// Project header
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

			cursor := "  "
			if filterIdx == m.searchCursor {
				cursor = "> "
			}

			// MRU slot number
			numberPrefix := "   "
			for slot, name := range m.numberedSlots {
				if name == sess.name {
					numberPrefix = fmt.Sprintf("%d. ", slot)
					break
				}
			}

			line := fmt.Sprintf("%s%s %s", cursor, numberPrefix, sess.name)
			if filterIdx == m.searchCursor {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("170")).
		Padding(0, 1).
		Width(40).
		MarginLeft(2)
	b.WriteString(searchBox.Render("/ " + m.searchInput.View()))

	return b.String()
}
```

**Step 6: Add footer for search state**

In the footer switch in `View()`, add:

```go
case stateSearch:
	footer = footerStyle.Width(m.width).Render("↑/↓: navigate • enter: jump to session • esc: cancel search")
```

**Step 7: Run the app manually to verify**

Run: `go build -o /tmp/devx-test . && /tmp/devx-test`
Verify: Press `/`, type a few chars, see filtered results, Enter to jump, Esc to cancel.

**Step 8: Commit**

```bash
git add tui/model.go
git commit -m "feat: add fuzzy search mode with / key"
```

---

### Task 6: Update footer and help text

**Files:**
- Modify: `tui/model.go`

**Step 1: Update the stateList footer**

Change the footer text (around line 962) from:

```
"↑/↓: navigate • 1-9: jump • enter: attach • ..."
```

To:

```
"↑/↓: navigate • 1-9: jump (MRU) • /: search • enter: attach • c: create • d: delete • o: open routes • e: edit • h: hostnames • P: projects • p: preview • ?: help • q: quit"
```

**Step 2: Update FullHelp**

Add `Search` to the `FullHelp()` key bindings return so it shows in extended help.

**Step 3: Commit**

```bash
git add tui/model.go
git commit -m "feat: update footer and help text with MRU and search hints"
```

---

### Task 7: Final verification

**Step 1: Run full pre-commit checklist**

```bash
gofmt -w .
go vet ./...
golangci-lint run --timeout=5m
go test -v -race ./...
go mod tidy
```

**Step 2: Fix any issues found**

**Step 3: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix: address lint and test issues"
```
