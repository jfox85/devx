# DevX Operator Console v2

A static, interactive shell study for operating multiple DevX sessions without replacing the terminal. V2 keeps the original Operator Console lane, but aligns it with the current app’s real capabilities and data model.

## V2 rationale

- **Blue/cyan is the product action and selection system.** It identifies the DevX mark, selected session, selected tab, focus, buttons, mobile navigation, and composer actions. Green is reserved for active/running/success. Amber and red retain warning/repair meaning.
- **The session navigator is the primary index.** It supports project grouping, collapse, filter, New session, status/target/artifact metadata, stale review, and session actions without introducing global destinations that do not exist.
- **Terminal remains the dominant surface.** The status disclosure is closed by default and docks beside the terminal when open. It only reports current concepts: derived status/reasons, target, route addresses, Gatepost log availability, and artifact count.
- **Capability completeness uses progressive disclosure.** Lifecycle actions live in session/stale dialogs; terminal and artifact actions live in the window toolbar and mobile action menu; rare shell states live in a state gallery.
- **The structural breakpoint follows production.** Below 1024px the navigator becomes a focus-managed modal sheet. Below 1181px Status and Artifacts cannot compete for width; Status overlays the workspace and opening either surface closes the other. At phone widths they become mutually exclusive full destinations with focus entry and isolated background controls.

## Interactions to try

- Filter sessions; `/` focuses filtering only when focus is not in an editable control.
- Press `Cmd/Ctrl+P` for the quick switcher and `Cmd/Ctrl+K` for compose.
- Collapse projects, select a session, switch terminal tabs, and use arrow keys within the tablist.
- Open the closed-by-default **Status** panel to inspect status reasons, target, route addresses, Gatepost log placement, and artifacts.
- Cycle Split through **terminal → vertical → horizontal → artifacts → terminal**.
- Open session actions to exercise validated rename, color selection, routes/status, share-target validation, and two-step delete/progress placement.
- Open stale review for mark reviewed, repair-session selection, and confirm/prune preview states. The unavailable fixture row is explicitly disabled.
- Use terminal actions for scoped output, validated artifact creation, reference insertion, artifact panes, disabled upload-placement guidance, split, and validated compose.
- At phone width, open the sessions sheet, action menu, docked multiline composer, paste-only/send actions, and soft-keybar.
- Open **View UI states** at the navigator footer. The remote-image and flag cards launch the real app-level toast placements, including preview/open/dismiss and flagged-session navigation.
- Drag/drop or paste an image to exercise the simulated image confirmation toast.

## Accessibility and responsive notes

- Operational text is 11–13px with raised muted contrast; meaningful state is never color-only.
- Controls expose `aria-expanded`, `aria-current`, `aria-selected`, or `aria-modal` where applicable.
- Terminal windows use tab semantics and left/right arrow navigation.
- The closed mobile session sheet is inert and accessibility-hidden. Open state moves focus into filtering, traps Tab, makes the workspace and mobile dock inert, and restores the invoking control on close.
- Mobile Status and Artifacts move focus to their headings, isolate obscured controls, remain mutually exclusive, and restore their trigger on Escape.
- Dialogs use native modal focus behavior. Popovers dismiss one top layer at a time and restore their trigger. Coarse pointers receive 44px targets.
- The shell is viewport-bound with internal navigator, terminal, panel, and dialog scrolling. Automated coverage includes 1440×1000, 1024×768, 900×800, 768×1024, 700×800, 601×800, 390×844, and 320×700 with no document overflow.

## Truthfulness boundaries

This prototype does **not** claim session runtime or session age, ahead/behind count, exact changed-file breakdown, or route health. It does not add global Attention, Artifacts, Routes, Settings, or profile destinations. Route presence is explicitly shown as an address, not a health check. Session color remains a user-assigned identifier, distinct from status and selection.

No production files are changed. Session fixtures are complete and selection re-renders identity, facts, terminal, status, routes, artifacts, and Gatepost placement together. Validated local transitions update the prototype fixture; unavailable upload/delete/share execution is explicitly described as placement or preview behavior. API calls, SSE, ttyd iframe behavior, upload, cleanup, and token execution remain production responsibilities.

Run `python3 design-prototypes/operator-console/validate.py` while serving this directory on `http://127.0.0.1:4181/` to exercise the viewport, fixture, action, layer, and mobile accessibility regression suite.

See [UI-INVENTORY.md](./UI-INVENTORY.md) for the production capability mapping and [../HARNESS-RESEARCH.md](../HARNESS-RESEARCH.md) for explicitly sequenced post-shell research.
