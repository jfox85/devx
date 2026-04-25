# Session Display Name & Color Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add display name and color indicator fields to devx sessions, surfaced in CLI, TUI, and web interfaces.

**Architecture:** Two new fields (`DisplayName`, `Color`) on the `Session` struct. Color logic (palette, validation, auto-assignment) in `session/color.go`. Display name resolution in `session/display.go`. New CLI commands `session rename` and `session color`. TUI and web interfaces updated to show colored dots and display names, with inline editing.

**Tech Stack:** Go (Cobra CLI, Bubble Tea TUI, lipgloss styling), Svelte (web frontend), SSE (real-time updates)

**Spec:** `docs/superpowers/specs/2026-04-24-session-display-name-color-design.md`

---

### Task 1: Color utilities — `session/color.go`

**Files:**
- Create: `session/color.go`
- Create: `session/color_test.go`

- [ ] **Step 1: Write failing tests for color utilities**

Create `session/color_test.go`:

```go
package session

import (
	"fmt"
	"testing"
)

func TestIsValidColor(t *testing.T) {
	valid := []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}
	for _, c := range valid {
		if !IsValidColor(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}

	invalid := []string{"", "RED", "Red", "magenta", "white", "black", "#ff0000", "123"}
	for _, c := range invalid {
		if IsValidColor(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

func TestAutoColor(t *testing.T) {
	// Deterministic: same name always gets the same color
	c1 := AutoColor("my-session")
	c2 := AutoColor("my-session")
	if c1 != c2 {
		t.Errorf("AutoColor not deterministic: got %q and %q", c1, c2)
	}

	// Result is always a valid color
	names := []string{"a", "test", "feature/my-branch", "x/y/z", ""}
	for _, name := range names {
		c := AutoColor(name)
		if !IsValidColor(c) {
			t.Errorf("AutoColor(%q) returned invalid color %q", name, c)
		}
	}

	// Different names should produce some variety (not all the same)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		seen[AutoColor(fmt.Sprintf("session-%d", i))] = true
	}
	if len(seen) < 3 {
		t.Errorf("AutoColor lacks variety: only %d distinct colors from 100 inputs", len(seen))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/ -run "TestIsValidColor|TestAutoColor" -v`
Expected: FAIL — `IsValidColor` and `AutoColor` not defined

- [ ] **Step 3: Implement color utilities**

Create `session/color.go`:

```go
package session

import "hash/fnv"

// Palette is the set of valid session colors, matching Claude Code's palette.
var Palette = []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}

// IsValidColor returns true if c is a recognized session color.
func IsValidColor(c string) bool {
	for _, p := range Palette {
		if c == p {
			return true
		}
	}
	return false
}

// AutoColor deterministically assigns a color to a session name by hashing.
func AutoColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return Palette[h.Sum32()%uint32(len(Palette))]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/ -run "TestIsValidColor|TestAutoColor" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/color.go session/color_test.go
git commit -m "feat: add session color palette, validation, and auto-assignment"
```

---

### Task 2: Display name helper — `session/display.go`

**Files:**
- Create: `session/display.go`
- Create: `session/display_test.go`

- [ ] **Step 1: Write failing tests for Label()**

Create `session/display_test.go`:

```go
package session

import (
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		want        string
	}{
		{name: "my-session", displayName: "", want: "my-session"},
		{name: "my-session", displayName: "My Feature", want: "My Feature"},
		{name: "jf-add-web", displayName: "Web UI", want: "Web UI"},
	}
	for _, tt := range tests {
		s := &Session{Name: tt.name, DisplayName: tt.displayName}
		got := s.Label()
		if got != tt.want {
			t.Errorf("Session{Name:%q, DisplayName:%q}.Label() = %q, want %q",
				tt.name, tt.displayName, got, tt.want)
		}
	}
}

func TestIsValidDisplayName(t *testing.T) {
	// Valid display names
	valid := []string{"My Feature", "a", "Web UI Feature", strings.Repeat("x", 100)}
	for _, dn := range valid {
		if !IsValidDisplayName(dn) {
			t.Errorf("expected %q to be valid display name", dn)
		}
	}

	// Invalid: too long
	if IsValidDisplayName(strings.Repeat("x", 101)) {
		t.Error("expected 101-char display name to be invalid")
	}

	// Empty is valid (means "clear")
	if !IsValidDisplayName("") {
		t.Error("expected empty display name to be valid")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/ -run "TestLabel|TestIsValidDisplayName" -v`
Expected: FAIL — `Label` and `IsValidDisplayName` not defined

- [ ] **Step 3: Implement display helpers**

Create `session/display.go`:

```go
package session

// MaxDisplayNameLen is the maximum length of a session display name.
const MaxDisplayNameLen = 100

// Label returns the display name if set, otherwise the session name.
func (s *Session) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// IsValidDisplayName returns true if dn is a valid display name.
// Empty is valid (used to clear). Max length is 100 characters.
func IsValidDisplayName(dn string) bool {
	return len(dn) <= MaxDisplayNameLen
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/ -run "TestLabel|TestIsValidDisplayName" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add session/display.go session/display_test.go
git commit -m "feat: add session display name Label() and validation"
```

---

### Task 3: Add fields to Session struct

**Files:**
- Modify: `session/metadata.go:16-31` (Session struct)

- [ ] **Step 1: Write failing test for new fields**

Add to `session/metadata_test.go`:

```go
func TestSessionDisplayNameAndColor(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("test-sess", "main", "/path", map[string]int{})

	// Set display name and color via UpdateSession
	err = store.UpdateSession("test-sess", func(s *Session) {
		s.DisplayName = "My Feature"
		s.Color = "blue"
	})
	if err != nil {
		t.Fatalf("failed to update session: %v", err)
	}

	// Reload and verify persistence
	store2, _ := LoadSessions()
	sess, _ := store2.GetSession("test-sess")
	if sess.DisplayName != "My Feature" {
		t.Errorf("expected DisplayName 'My Feature', got %q", sess.DisplayName)
	}
	if sess.Color != "blue" {
		t.Errorf("expected Color 'blue', got %q", sess.Color)
	}
	if sess.Label() != "My Feature" {
		t.Errorf("expected Label() 'My Feature', got %q", sess.Label())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./session/ -run TestSessionDisplayNameAndColor -v`
Expected: FAIL — `DisplayName` and `Color` fields don't exist on Session

- [ ] **Step 3: Add fields to Session struct**

In `session/metadata.go`, add two fields to the `Session` struct after the `AttentionTime` field:

```go
DisplayName string `json:"display_name,omitempty"`
Color       string `json:"color,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./session/ -run TestSessionDisplayNameAndColor -v`
Expected: PASS

- [ ] **Step 5: Run all session tests to check for regressions**

Run: `go test ./session/ -v`
Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
git add session/metadata.go session/metadata_test.go
git commit -m "feat: add DisplayName and Color fields to Session struct"
```

---

### Task 4: CLI — `devx session rename` command

**Files:**
- Create: `cmd/session_rename.go`
- Create: `cmd/notify.go` (extract generic web server notification helper)

- [ ] **Step 1: Create generic session-updated notification helper**

The existing `notifyWebServer` in `cmd/session_flag.go` fires a `flag` SSE event, which is semantically wrong for rename/color changes. Create `cmd/notify.go` with a generic notification that triggers a session list refresh:

```go
package cmd

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/viper"
)

// notifySessionUpdated fires a POST to /api/sessions/flag-notify with
// flagged=false so the web UI re-polls its session list. This is a
// lightweight signal that session metadata changed — the browser's
// background poll (5s) would eventually pick it up, but this makes it
// immediate.
func notifySessionUpdated(name string) {
	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")
	if token == "" || port == 0 {
		return
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("flagged", "false")
	addr := fmt.Sprintf("http://localhost:%d/api/sessions/flag-notify?%s", port, q.Encode())
	req, err := http.NewRequest(http.MethodPost, addr, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
```

Note: This reuses the existing `flag-notify` endpoint to trigger a browser-side refresh. The web frontend already re-polls sessions on any `flag` SSE event (via `refreshTrigger`), so this works without needing a new SSE event type.

- [ ] **Step 2: Create the rename command**

Create `cmd/session_rename.go`:

```go
package cmd

import (
	"fmt"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var clearDisplayNameFlag bool

var sessionRenameCmd = &cobra.Command{
	Use:   "rename <session-name> [display-name]",
	Short: "Set or clear the display name for a session",
	Long: `Set a display name for a session. The display name is shown in the TUI,
web interface, and CLI list instead of the internal session name.

Use --clear to remove the display name and revert to the internal name.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSessionRename,
}

func init() {
	sessionCmd.AddCommand(sessionRenameCmd)
	sessionRenameCmd.Flags().BoolVar(&clearDisplayNameFlag, "clear", false, "Clear the display name")
}

func runSessionRename(cmd *cobra.Command, args []string) error {
	sessionName := args[0]

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	if _, exists := store.GetSession(sessionName); !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	if clearDisplayNameFlag {
		if err := store.UpdateSession(sessionName, func(s *session.Session) {
			s.DisplayName = ""
		}); err != nil {
			return fmt.Errorf("failed to clear display name: %w", err)
		}
		fmt.Printf("Cleared display name for session '%s'\n", sessionName)
		notifySessionUpdated(sessionName)
		return nil
	}

	if len(args) < 2 {
		return fmt.Errorf("display name required (or use --clear to remove)")
	}

	displayName := args[1]
	if !session.IsValidDisplayName(displayName) {
		return fmt.Errorf("display name too long (max %d characters)", session.MaxDisplayNameLen)
	}

	if err := store.UpdateSession(sessionName, func(s *session.Session) {
		s.DisplayName = displayName
	}); err != nil {
		return fmt.Errorf("failed to set display name: %w", err)
	}

	fmt.Printf("Set display name for session '%s' to '%s'\n", sessionName, displayName)
	notifySessionUpdated(sessionName)
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add cmd/notify.go cmd/session_rename.go
git commit -m "feat: add 'devx session rename' command for display names"
```

---

### Task 5: CLI — `devx session color` command

**Files:**
- Create: `cmd/session_color.go`

- [ ] **Step 1: Create the color command**

Create `cmd/session_color.go`:

```go
package cmd

import (
	"fmt"
	"strings"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var sessionColorCmd = &cobra.Command{
	Use:   "color <session-name> <color>",
	Short: "Set the color indicator for a session",
	Long: fmt.Sprintf(`Set the color indicator for a session. Available colors: %s`,
		strings.Join(session.Palette, ", ")),
	Args: cobra.ExactArgs(2),
	RunE: runSessionColor,
}

func init() {
	sessionCmd.AddCommand(sessionColorCmd)
}

func runSessionColor(cmd *cobra.Command, args []string) error {
	sessionName := args[0]
	color := args[1]

	if !session.IsValidColor(color) {
		return fmt.Errorf("invalid color %q. Valid colors: %s", color, strings.Join(session.Palette, ", "))
	}

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	if _, exists := store.GetSession(sessionName); !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	if err := store.UpdateSession(sessionName, func(s *session.Session) {
		s.Color = color
	}); err != nil {
		return fmt.Errorf("failed to set color: %w", err)
	}

	fmt.Printf("Set color for session '%s' to '%s'\n", sessionName, color)
	notifySessionUpdated(sessionName)
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add cmd/session_color.go
git commit -m "feat: add 'devx session color' command"
```

---

### Task 6: Modify `session create` — auto-assign color

**Files:**
- Modify: `cmd/session_create.go:17-23` (flag vars), `cmd/session_create.go:33-41` (init), `cmd/session_create.go:219` (after AddSessionWithProject)

- [ ] **Step 1: Add flags and auto-assign logic**

In `cmd/session_create.go`:

1. Add flag variables alongside existing ones:

```go
var (
	createColorFlag       string
	createDisplayNameFlag string
)
```

2. In `init()`, add the new flags:

```go
sessionCreateCmd.Flags().StringVar(&createColorFlag, "color", "", "Session color (auto-assigned if not specified)")
sessionCreateCmd.Flags().StringVar(&createDisplayNameFlag, "display-name", "", "Display name for the session")
```

3. **Early in `runSessionCreate`** (after the name validation, before any side effects like worktree creation), validate the flags:

```go
// Validate --color and --display-name flags early (before side effects)
if createColorFlag != "" && !session.IsValidColor(createColorFlag) {
	return fmt.Errorf("invalid color %q. Valid colors: %s", createColorFlag, strings.Join(session.Palette, ", "))
}
if createDisplayNameFlag != "" && !session.IsValidDisplayName(createDisplayNameFlag) {
	return fmt.Errorf("display name too long (max 100 characters)")
}
```

4. After the `store.AddSessionWithProject(...)` call (around line 219), set color and display name:

```go
// Set color (auto-assign if not specified) — flag already validated above
color := createColorFlag
if color == "" {
	color = session.AutoColor(name)
}
if err := store.UpdateSession(name, func(s *session.Session) {
	s.Color = color
	if createDisplayNameFlag != "" {
		s.DisplayName = createDisplayNameFlag
	}
}); err != nil {
	fmt.Printf("Warning: failed to set color/display-name: %v\n", err)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add cmd/session_create.go
git commit -m "feat: auto-assign color on session create, add --color and --display-name flags"
```

---

### Task 7: Modify `session list` — colored dots and display names

**Files:**
- Modify: `cmd/session_list.go:28-36` (SessionStatus struct), `cmd/session_list.go:58-65` (status construction), `cmd/session_list.go:178-249` (displaySessionList)

- [ ] **Step 1: Update SessionStatus struct and list construction**

Add fields to `SessionStatus`:

```go
type SessionStatus struct {
	Name         string
	DisplayName  string
	Color        string
	// ... existing fields ...
}
```

In `runSessionList`, when building each `SessionStatus`, add:

```go
status.DisplayName = sess.DisplayName  // raw value — empty means "not set"
status.Color = sess.Color
if status.Color == "" {
	status.Color = session.AutoColor(name)
}
```

- [ ] **Step 2: Update displaySessionList for colored output**

Replace the name column rendering in `displaySessionList`. Add ANSI color mapping:

```go
var ansiColors = map[string]string{
	"red": "\033[31m", "blue": "\033[34m", "green": "\033[32m", "yellow": "\033[33m",
	"purple": "\033[35m", "orange": "\033[38;5;208m", "pink": "\033[38;5;213m", "cyan": "\033[36m",
}

const ansiReset = "\033[0m"
```

Update the header and row rendering to show a colored dot and display name:

```go
// In the header:
fmt.Fprintln(w, "  NAME\tBRANCH\tPORTS\tHOSTS\tSTATUS")
fmt.Fprintln(w, "  ----\t------\t-----\t-----\t------")

// In the row:
dot := "●"
if c, ok := ansiColors[status.Color]; ok {
	dot = c + "●" + ansiReset
}

// Show display name with real name in parens when set
nameDisplay := status.Name
if status.DisplayName != "" {
	nameDisplay = fmt.Sprintf("%s (%s)", status.DisplayName, status.Name)
}

fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\t%s\n",
	dot,
	nameDisplay,
	// ... rest unchanged ...
)
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add cmd/session_list.go
git commit -m "feat: show colored dots and display names in session list"
```

---

### Task 8: TUI — colored dots and display names in session list

**Files:**
- Modify: `tui/model.go:30-43` (sessionItem struct), rendering logic
- Modify: `tui/styles.go` (add color mapping)

- [ ] **Step 1: Add color mapping to styles.go**

Add to `tui/styles.go`:

```go
// SessionColorStyles maps session color names to lipgloss styles for the dot indicator.
var SessionColorStyles = map[string]lipgloss.Style{
	"red":    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	"blue":   lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
	"green":  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	"yellow": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	"purple": lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
	"orange": lipgloss.NewStyle().Foreground(lipgloss.Color("208")),
	"pink":   lipgloss.NewStyle().Foreground(lipgloss.Color("213")),
	"cyan":   lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
}
```

- [ ] **Step 2: Add fields to sessionItem struct**

In `tui/model.go`, add to `sessionItem`:

```go
displayName    string // raw DisplayName from metadata (empty if not set)
color          string
```

- [ ] **Step 3: Update session loading to populate new fields**

Find where `sessionItem` instances are created (in the `refreshSessions` or similar function) and add:

```go
item.displayName = sess.DisplayName  // raw value, not Label() — we need to distinguish "not set" from "same as name"
item.color = sess.Color
if item.color == "" {
	item.color = session.AutoColor(sess.Name)
}
```

- [ ] **Step 4: Update session rendering to show colored dot and display name**

Find the session list rendering code (the `View()` method or the function that renders each session row). Before each session name, add:

```go
dotStyle, ok := SessionColorStyles[item.color]
if !ok {
	dotStyle = dimStyle
}
dot := dotStyle.Render("●")

// Show display name if set, with real name in dimmed parens
label := item.name
if item.displayName != "" {
	label = item.displayName + " " + dimStyle.Render("("+item.name+")")
}
```

Use `dot + " " + label` in place of just the session name.

- [ ] **Step 5: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
git add tui/model.go tui/styles.go
git commit -m "feat: show colored dots and display names in TUI session list"
```

---

### Task 9: TUI — rename (`r`) and color cycle (`K`) keybindings

**Files:**
- Modify: `tui/model.go` (keyMap, Update, View)

- [ ] **Step 1: Add keybindings to keyMap**

Find the `keyMap` struct and add:

```go
Rename key.Binding
ColorCycle key.Binding
```

In the keyMap initialization, add:

```go
Rename: key.NewBinding(
	key.WithKeys("r"),
	key.WithHelp("r", "rename"),
),
ColorCycle: key.NewBinding(
	key.WithKeys("K"),
	key.WithHelp("K", "cycle color"),
),
```

- [ ] **Step 2: Add a new state for renaming**

Add to the `state` enum:

```go
stateRenaming
```

- [ ] **Step 3: Handle `r` keypress in stateList**

In the `Update` method's `stateList` key handling section, add a case for the Rename keybinding:

```go
case key.Matches(msg, m.keys.Rename):
	if len(m.sessions) > 0 {
		sess := m.sessions[m.cursor]
		m.state = stateRenaming
		// Pre-fill with current display name, or session name if not set
		prefill := sess.displayName
		if prefill == "" {
			prefill = sess.name
		}
		m.textInput.SetValue(prefill)
		m.textInput.Focus()
	}
```

- [ ] **Step 4: Handle `K` keypress in stateList**

Add a case for the ColorCycle keybinding (note: `C` is already bound to `ClaudeHooks`, so we use `K` for "kolor"):

```go
case key.Matches(msg, m.keys.ColorCycle):
	if len(m.sessions) > 0 {
		sess := m.sessions[m.cursor]
		// Find current color index and advance
		nextIdx := 0
		for i, c := range session.Palette {
			if c == sess.color {
				nextIdx = (i + 1) % len(session.Palette)
				break
			}
		}
		newColor := session.Palette[nextIdx]
		store, err := session.LoadSessions()
		if err == nil {
			_ = store.UpdateSession(sess.name, func(s *session.Session) {
				s.Color = newColor
			})
			m.sessions[m.cursor].color = newColor
		}
	}
```

- [ ] **Step 5: Handle stateRenaming input**

Add a new case block in `Update` for `stateRenaming`:

```go
case stateRenaming:
	switch {
	case key.Matches(msg, m.keys.Back):
		m.state = stateList
		m.textInput.Blur()
	case key.Matches(msg, m.keys.Enter):
		newName := m.textInput.Value()
		if session.IsValidDisplayName(newName) {
			sess := m.sessions[m.cursor]
			// If user typed the session name itself, treat as clearing the display name
			displayName := newName
			if displayName == sess.name {
				displayName = ""
			}
			store, err := session.LoadSessions()
			if err == nil {
				_ = store.UpdateSession(sess.name, func(s *session.Session) {
					s.DisplayName = displayName
				})
				m.sessions[m.cursor].displayName = displayName
			}
		}
		m.state = stateList
		m.textInput.Blur()
	default:
		m.textInput, _ = m.textInput.Update(msg)
	}
```

- [ ] **Step 6: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add tui/model.go
git commit -m "feat: add rename (r) and color cycle (K) keybindings to TUI"
```

---

### Task 10: Web API — new fields and endpoints

**Files:**
- Modify: `web/api.go:90-98` (sessionResponse struct), `web/api.go:100-120` (buildSessionResponse), `web/api.go:26-42` (registerAPIRoutes)

- [ ] **Step 1: Update sessionResponse struct**

In `web/api.go`, add to `sessionResponse`:

```go
DisplayName string `json:"display_name,omitempty"`
Color       string `json:"color"`
```

- [ ] **Step 2: Update buildSessionResponse**

In `buildSessionResponse`, add:

```go
color := sess.Color
if color == "" {
	color = session.AutoColor(sess.Name)
}

return sessionResponse{
	// ... existing fields ...
	DisplayName: sess.DisplayName,
	Color:       color,
}
```

- [ ] **Step 3: Add rename endpoint handler**

Add to `web/api.go`:

```go
func handleRenameSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	displayName := r.URL.Query().Get("display_name")
	args := []string{"session", "rename"}
	if displayName == "" {
		args = append(args, "--clear", "--", name)
	} else {
		args = append(args, "--", name, displayName)
	}
	if err := runSelf(args...); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Add color endpoint handler**

Add to `web/api.go`:

```go
func handleColorSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	color := r.URL.Query().Get("color")
	if name == "" || color == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and color query params required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if err := runSelf("session", "color", "--", name, color); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Register new routes**

In `web/api.go` `registerAPIRoutes`, add after the existing routes (these handlers don't need the SSE hub, so they belong here alongside other non-SSE API handlers):

```go
mux.HandleFunc("POST /api/sessions/rename", handleRenameSession)
mux.HandleFunc("POST /api/sessions/color", handleColorSession)
```

- [ ] **Step 6: Verify it compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add web/api.go
git commit -m "feat: add rename and color API endpoints, include fields in session response"
```

---

### Task 11: Web frontend — add API functions

**Files:**
- Modify: `web/app/src/api.js`

- [ ] **Step 1: Add renameSession and colorSession API functions**

Add to `web/app/src/api.js`:

```javascript
export async function renameSession(name, displayName) {
  const params = new URLSearchParams({ name })
  if (displayName != null) params.set('display_name', displayName)
  const res = await apiFetch('/sessions/rename?' + params.toString(), { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    throw new Error(err.error || 'Rename failed')
  }
}

export async function colorSession(name, color) {
  const res = await apiFetch(
    '/sessions/color?name=' + encodeURIComponent(name) + '&color=' + encodeURIComponent(color),
    { method: 'POST' }
  )
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    throw new Error(err.error || 'Color change failed')
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add web/app/src/api.js
git commit -m "feat: add renameSession and colorSession API client functions"
```

---

### Task 12: Web frontend — colored dots, display names, inline editing

**Files:**
- Modify: `web/app/src/lib/SessionList.svelte`

- [ ] **Step 1: Add color CSS mapping and state variables**

At the top of the `<script>` block, add:

```javascript
import { renameSession, colorSession } from '../api.js'

const colorMap = {
  red: '#ef4444', blue: '#3b82f6', green: '#22c55e', yellow: '#eab308',
  purple: '#a855f7', orange: '#f97316', pink: '#ec4899', cyan: '#06b6d4',
}
const colorOrder = ['red', 'blue', 'green', 'yellow', 'purple', 'orange', 'pink', 'cyan']

let editingName = null   // session.name being renamed
let editValue = ''       // current text input value
```

- [ ] **Step 2: Add inline rename handlers**

```javascript
function startRename(session) {
  editingName = session.name
  editValue = session.display_name || session.name
}

async function submitRename(session) {
  const newName = editValue.trim()
  try {
    if (newName === '' || newName === session.name) {
      await renameSession(session.name, null)  // clear
    } else {
      await renameSession(session.name, newName)
    }
    await load({ background: true })
  } catch (e) {
    error = e.message
  }
  editingName = null
}

function cancelRename() {
  editingName = null
}

async function cycleColor(session) {
  const currentIdx = colorOrder.indexOf(session.color || 'blue')
  const nextColor = colorOrder[(currentIdx + 1) % colorOrder.length]
  try {
    await colorSession(session.name, nextColor)
    // Optimistic update
    session.color = nextColor
    sessions = [...sessions]
  } catch (e) {
    error = e.message
  }
}
```

- [ ] **Step 3: Update session row template**

Replace the session name button content to include a colored dot and display name. In the session row area (around the `<button on:click={() => onOpenTerminal(session)}>` block):

Before the `<span class="flex-1 truncate leading-none">` element, add the colored dot:

```svelte
<!-- Color dot -->
<button
  on:click|stopPropagation={() => cycleColor(session)}
  class="shrink-0 text-[10px] hover:scale-125 transition-transform"
  style="color: {colorMap[session.color] || colorMap.blue}"
  title="click to change color"
>●</button>
```

Replace the name span with display-name-aware rendering:

```svelte
{#if editingName === session.name}
  <input
    bind:value={editValue}
    on:keydown={(e) => {
      if (e.key === 'Enter') { e.target.blur(); submitRename(session) }
      else if (e.key === 'Escape') cancelRename()
    }}
    on:blur={() => {
      // Only cancel if not submitting — Enter handler calls blur() then submitRename()
      // Use a microtask delay so the Enter keydown fires first
      setTimeout(() => { if (editingName === session.name) cancelRename() }, 0)
    }}
    class="flex-1 bg-transparent text-gray-200 text-sm lg:text-xs font-mono outline-none border-b border-cyan-800 min-w-0"
    autofocus
  />
{:else}
  <span
    class="flex-1 truncate leading-none cursor-text"
    on:dblclick|stopPropagation={() => startRename(session)}
    title="double-click to rename"
  >
    {session.display_name || session.name}
    {#if session.display_name && session.display_name !== session.name}
      <span class="text-gray-700 text-[10px] ml-1">({session.name})</span>
    {/if}
  </span>
{/if}
```

- [ ] **Step 4: Update the search filter to include display names**

Update the `filtered` reactive statement:

```javascript
$: filtered = sessions.filter(s =>
  !searchQuery || s.name.toLowerCase().includes(searchQuery.toLowerCase())
    || (s.display_name && s.display_name.toLowerCase().includes(searchQuery.toLowerCase()))
)
```

- [ ] **Step 5: Build the web frontend**

Run: `cd web/app && npm run build`
Expected: Build succeeds

- [ ] **Step 6: Commit**

```bash
git add web/app/src/lib/SessionList.svelte web/app/src/api.js
git commit -m "feat: add colored dots, display names, and inline editing to web session list"
```

---

### Task 13: Config change — update tmux template

**Files:**
- Modify: `.devx/session.yaml.tmpl:24`

- [ ] **Step 1: Update the editor pane command**

In `.devx/session.yaml.tmpl`, change the editor pane from:

```yaml
      - claude
```

to:

```yaml
      - clauded -n "{{.Name}}"
```

- [ ] **Step 2: Commit**

```bash
git add .devx/session.yaml.tmpl
git commit -m "config: use clauded with session name in tmux editor pane"
```

---

### Task 14: Run full test suite and quality checks

**Files:** None (verification only)

- [ ] **Step 1: Format all Go code**

Run: `gofmt -w .`

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Run golangci-lint**

Run: `golangci-lint run --timeout=5m`
Expected: No issues (or only pre-existing ones)

- [ ] **Step 4: Run all tests with race detection**

Run: `go test -v -race ./...`
Expected: All tests PASS

- [ ] **Step 5: Tidy modules**

Run: `go mod tidy`

- [ ] **Step 6: Build the binary**

Run: `make build`
Expected: Build succeeds

- [ ] **Step 7: Rebuild web frontend**

Run: `cd web/app && npm run build`
Expected: Build succeeds

- [ ] **Step 8: Commit any formatting/tidy changes**

```bash
git add -A && git diff --cached --quiet || git commit -m "chore: format and tidy"
```
