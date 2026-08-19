# Operator Console v3 UI inventory

This inventory maps current production shell behavior and direct user feedback to the v3 prototype. It documents existing capability placement, not new product scope.

| Capability / feedback | V3 location | Prototype treatment |
|---|---|---|
| Full lowercase brand | Navigator header | Literal `devx` wordmark |
| Large session fleet | Navigator | 25 truthful, fully selectable fixtures across four projects |
| Desktop row density | Navigator rows | One-line 32px rows; about 20 visible in a 1000px viewport |
| Touch row density | Navigator rows | 44px on coarse pointers/mobile |
| Status + user color | Every row | Separate semantic dot and personal-color square |
| Name, target, artifacts/attention/status | Every row | Compact name and target plus artifact count or textual status glyph |
| Branch and reasons | Hover/focus tooltip, header/facts, Status | Progressive disclosure; no permanently two-line rows |
| New session | Navigator header | Validated project, name, and target modal; creates a complete local fixture |
| Search/filter | Navigator search | Name, project, and branch; `/` ignores editable fields; arrow/Enter navigation |
| Quick switcher | `Cmd/Ctrl+P` | All complete fixture sessions; uses the same selection path |
| Project grouping/collapse | Navigator | Collapsible sections with counts and `aria-expanded` |
| Rename/session color | Session More | Validated rename and persistent identifier-color selection |
| Derived status/reasons | Row, facts, Status | Text and color; no invented telemetry |
| Target type | Row chip, facts, Status | Host/docker/gatepost |
| Routes/Gatepost | Status | Addresses only, never asserted healthy |
| Stale summary/review | Navigator disclosure | Clean/repair review, mark reviewed, repair selection, confirm/prune preview |
| Terminal windows | Window tablist | Tab semantics and left/right arrow navigation |
| Full-screen Output | First desktop action; compact Actions menu | Full viewport, title/session identity, Open Tab, Close, internal scroll, focus trap/restore, Escape |
| Desktop action hierarchy | Window bar | Stable labeled **Output · Artifacts · Split: mode · Compose · More** grouping |
| Tablet/mobile actions | Window bar | One labeled **Actions** control with descriptive menu; no mystery icon row |
| Artifact entry points | Artifacts action, facts, Status, mobile navigation | Session-scoped pane/destination |
| Artifact sorting | Artifact header/menu | Newest, oldest, and title options |
| Artifact list visibility | Artifact header/menu | Show/hide list; selected item remains previewed |
| Artifact list sizing | List divider | Pointer-draggable height concept with bounded range |
| Artifact selection | Artifact list | Selection state and synchronized inline preview |
| Image preview | Preview surface | Large contained image with descriptive alt text |
| Text/Markdown preview | Preview surface | Readable, internally scrolling monospace content |
| JSX preview/code | Selected JSX toolbar | Obvious **Preview** / **Code** toggle |
| Artifact full screen | Artifact header/menu | True full viewport with **Exit Full** and Escape |
| Upload | Artifact header/menu | Local fixture file intake; adds scoped preview items |
| New artifact | Artifact header/menu | Required text, optional title, JSX detection, scoped fixture update |
| Refresh | Artifact header/menu | Re-renders scoped list and provides feedback |
| Close | Artifact header/menu | Returns to terminal layout and restores focus |
| Insert selected item | Selected-item toolbar | Inserts artifact reference into desktop/mobile composer |
| Edit selected item | Selected-item toolbar | Validated title edit |
| Archive selected item | Selected-item toolbar | Visible archived state |
| Remove selected item | Selected-item toolbar | Removes the local fixture and selects the next available item |
| Split modes | Desktop labeled action | Terminal, vertical, horizontal, artifacts; current mode always named |
| Compose | Desktop labeled action; compact menu; `Cmd/Ctrl+K` | Desktop overlay plus mobile docked composer |
| Image input | More/Actions; paste/drop | Explicit static confirmation boundary |
| Mobile soft keys | Below docked composer | Toggle and representative production keys |
| Panel exclusivity | Compact layout | Opening Status closes Artifacts and vice versa |
| Mobile navigator | Modal session sheet | Closed state inert/hidden; open focus trap and restoration |
| Dialogs/toasts | App level | Native modal behavior and reachable remote-image/flag toast actions |
| Loading/error/empty/reconnect/deleting | State gallery | Retained shell-state documentation |

## Compact fleet contract

Desktop rows are intentionally 32px and one line. Each row retains the minimum scan set: semantic state, user color, display name, target, and either artifact count or status signal. Full branch and status reason are available in the row title/selected-row hover or focus disclosure and in persistent selected-session surfaces. This mirrors the real high-density operating pattern rather than optimizing for a small demo list.

The initial fleet contains 25 complete objects. Selecting any row updates all dependent surfaces, including scoped artifacts. The validation suite checks total fixture count, row height, visible fleet density, search, keyboard navigation, grouping, and selection.

## Output and action contract

Output is not a centered modal. It is fixed to all four viewport edges and contains a clear heading, session context, Open Tab, Close, readable scrolling transcript, focus isolation, Tab trapping, Escape close, and focus restoration.

Desktop actions are words, not ambiguous symbols. Tablet/mobile intentionally collapse to one labeled Actions control. Artifact creation and insertion are owned by the Artifacts surface, where their context is clear.

## Data intentionally not claimed

- Session runtime, uptime, or unlabeled session age
- Git ahead/behind counts
- Exact modified/untracked file breakdown
- Route health
- Global Attention, Artifacts, Routes, Settings, or profile destinations

## Validation matrix

`validate.py` covers 12 viewports from 1440×1000 through 320×700, including requested 1440×1000, 1024×768, 768×1024, 390×844, and 320×700 sizes plus intermediate widths. It asserts containment, 32/44px density, complete fleet selection, labeled action modes, full-viewport Output geometry/focus, ArtifactPane sorting/list/preview/JSX/full-screen and item actions, responsive menus, panel exclusivity, mobile composer/soft keys, and zero console/page errors.
