# Session Display Name & Color Indicator

## Problem

Sessions are identified by their internal name (which doubles as the git branch name). Once created, this name is immutable. As sessions evolve, the original name may no longer reflect the work being done. Additionally, when scanning a list of sessions, there's no quick visual differentiator beyond reading the name text.

## Solution

Two new session metadata fields: **display name** (user-facing label) and **color** (visual indicator). The internal name, branch, and worktree path remain unchanged.

## Non-Goals

- Renaming the underlying git branch or worktree path
- Color pickers or custom hex values
- Persisting color across projects (colors are per-session)

---

## Data Model

Two new fields on `Session` in `session/metadata.go`:

```go
DisplayName string `json:"display_name,omitempty"`
Color       string `json:"color,omitempty"`
```

### Color Palette & Validation

8 colors (matching Claude Code's set): `red`, `blue`, `green`, `yellow`, `purple`, `orange`, `pink`, `cyan`

Color logic lives in a new file `session/color.go`:

```go
var Palette = []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}

func IsValidColor(c string) bool { /* check against Palette */ }

func AutoColor(name string) string {
    h := fnv.New32a()
    h.Write([]byte(name))
    return Palette[h.Sum32()%uint32(len(Palette))]
}
```

`IsValidColor` is the single source of truth used by CLI commands, web API handlers, and TUI input. `AutoColor` is deterministic (same name always gets the same color) and requires no state tracking.

### Display Name Resolution & Validation

A helper method on `Session` (in `session/display.go`):

```go
func (s *Session) Label() string {
    if s.DisplayName != "" {
        return s.DisplayName
    }
    return s.Name
}
```

Display names are capped at 100 characters (matching session name limit). All user-facing display points call `Label()` instead of accessing `Name` directly.

---

## CLI Commands

### New: `devx session rename <name> <display-name>`

- Sets the display name for an existing session
- `--clear` flag to remove display name (consistent with `session flag --clear` pattern)
- Fires SSE notification to web server (via `notifyWebServer`) so connected browsers update immediately
- Example: `devx session rename jf-add-web "Web UI Feature"`
- Example: `devx session rename jf-add-web --clear`

### New: `devx session color <name> <color>`

- Sets the color override for a session
- Validates against the palette via shared `session.IsValidColor()`
- Fires SSE notification to web server so connected browsers update immediately
- Example: `devx session color jf-add-web blue`

### Modified: `devx session create`

- Auto-assigns color via hash at creation time
- New `--color` flag to override auto-assignment
- New `--display-name` flag to set display name upfront

### Modified: `devx session list`

- Shows colored dot (ANSI) before each session name
- Shows display name with real name in parentheses when they differ
- Example output:

```
 ● Web UI Feature (jf-add-web)     devx   blue
 ● jf-update-services              devx   green
```

---

## TUI Changes

### Session List Rendering

- Colored dot (lipgloss-styled) before each session name
- Display `Label()` output (display name if set, else real name)
- When display name is set, show real name in dimmed text

### Keybindings

- `r` — Rename selected session. Opens inline text input. Pre-fills current display name if one exists.
- `K` (shift-k) — Cycle color for selected session. Each press advances to the next palette color, immediately updating the dot. (Lowercase `c` is already bound to "create new".)

---

## Web Interface

### Session List (`SessionList.svelte`)

- Colored dot/circle before each session name (CSS)
- Show display name if set, real name otherwise
- Real name shown in smaller/dimmed text when display name differs

### API Changes (`web/api.go`)

`sessionResponse` gets two new fields:

```go
DisplayName string `json:"display_name,omitempty"`
Color       string `json:"color"`
```

Two new endpoints (both delegate to CLI via `runSelf()`, consistent with existing create/delete/flag pattern):

- `POST /api/sessions/rename?name=<session>&display_name=<new-label>` — calls `runSelf("session", "rename", ...)`
- `POST /api/sessions/color?name=<session>&color=<color>` — calls `runSelf("session", "color", ...)`

Web create modal: color/display-name support in the create form is out of scope for this spec. The auto-assigned color from the CLI is sufficient initially.

### Web UI Interactions

- Click session name to edit display name (inline text input)
- Click color dot to cycle through palette colors

---

## Config Change (Non-code)

Update `.devx/session.yaml.tmpl` across local devx projects. In the editor pane, replace:

```yaml
- claude
```

with:

```yaml
- clauded -n "{{.Name}}"
```

This sets the Claude Code session name to match the devx session name at startup and uses the `clauded` alias (yolo mode). This is a per-project config change, not a devx implementation change.

---

## Files to Modify

| File | Change |
|------|--------|
| `session/metadata.go` | Add `DisplayName`, `Color` fields to `Session` struct |
| `session/color.go` | New file: `Palette`, `IsValidColor()`, `AutoColor()` |
| `session/display.go` | New file: `Label()` method |
| `cmd/session_create.go` | Auto-assign color; add `--color`, `--display-name` flags |
| `cmd/session_list.go` | Show colored dot and display name; update `SessionStatus` struct |
| `cmd/session_rename.go` | New command (set display name) |
| `cmd/session_color.go` | New command (set/override color) |
| `tui/model.go` | Colored dots, display name rendering, keybindings; update `sessionItem` struct |
| `tui/styles.go` | Color-to-lipgloss mapping |
| `web/api.go` | New fields in `sessionResponse`; update `buildSessionResponse()`; new rename/color endpoints (via `runSelf`) |
| `web/app/src/lib/SessionList.svelte` | Colored dots, display name, inline edit, color cycling |
| `.devx/session.yaml.tmpl` | `clauded -n "{{.Name}}"` |
