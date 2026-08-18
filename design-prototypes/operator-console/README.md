# DevX Operator Console v2

A static, interactive shell study for operating multiple DevX sessions without replacing the terminal. V2 keeps the original Operator Console lane, but aligns it with the current app’s real capabilities and data model.

## V2 rationale

- **Blue/cyan is the product action and selection system.** It identifies the DevX mark, selected session, selected tab, focus, buttons, mobile navigation, and composer actions. Green is reserved for active/running/success. Amber and red retain warning/repair meaning.
- **The session navigator is the primary index.** It supports project grouping, collapse, filter, New session, status/target/artifact metadata, stale review, and session actions without introducing global destinations that do not exist.
- **Terminal remains the dominant surface.** The status disclosure is closed by default and docks beside the terminal when open. It only reports current concepts: derived status/reasons, target, route addresses, Gatepost log availability, and artifact count.
- **Capability completeness uses progressive disclosure.** Lifecycle actions live in session/stale dialogs; terminal and artifact actions live in the window toolbar and mobile action menu; rare shell states live in a state gallery.
- **The structural breakpoint follows production.** Below 1024px the navigator becomes a focus-managed modal sheet. At phone widths the multiline composer, paste-only/send actions, soft keys, and active mobile navigation are docked.

## Interactions to try

- Filter sessions; `/` focuses filtering only when focus is not in an editable control.
- Press `Cmd/Ctrl+P` for the quick switcher and `Cmd/Ctrl+K` for compose.
- Collapse projects, select a session, switch terminal tabs, and use arrow keys within the tablist.
- Open the closed-by-default **Status** panel to inspect status reasons, target, route addresses, Gatepost log placement, and artifacts.
- Cycle Split through **terminal → vertical → horizontal → artifacts → terminal**.
- Open session actions for rename, color, routes/status, share-target concept, and two-step delete/progress.
- Open stale review for mark reviewed, repair, and confirm/prune concepts.
- Use terminal actions for output view, new artifact, insert reference, artifact pane, image attach, split, and compose.
- At phone width, open the sessions sheet, action menu, docked multiline composer, paste-only/send actions, and soft-keybar.
- Open **View UI states** at the navigator footer for loading, error, empty, no-results, reconnect, delete progress, remote-image toast, flag toast, share target, and image-input states.
- Drag/drop or paste an image to exercise the simulated image confirmation toast.

## Accessibility and responsive notes

- Operational text is 11–13px with raised muted contrast; meaningful state is never color-only.
- Controls expose `aria-expanded`, `aria-current`, `aria-selected`, or `aria-modal` where applicable.
- Terminal windows use tab semantics and left/right arrow navigation.
- The mobile session sheet moves focus into filtering, traps Tab, makes the workspace and mobile dock inert, and restores the invoking control on close.
- Dialogs use native modal focus behavior. Coarse pointers receive 44px targets.
- The design supports 1440px desktop, 1024px compact desktop/tablet boundary, 768px tablet, and 390px phone without horizontal page overflow.

## Truthfulness boundaries

This prototype does **not** claim session runtime, ahead/behind count, exact changed-file breakdown, or route health. It does not add global Attention, Artifacts, Routes, Settings, or profile destinations. Route presence is explicitly shown as an address, not a health check. Session color remains a user-assigned identifier, distinct from status and selection.

No production files are changed. The static interactions simulate placement and behavior; API calls, SSE, ttyd iframe behavior, upload, delete, repair, and share-token execution remain production responsibilities.

See [UI-INVENTORY.md](./UI-INVENTORY.md) for the production capability mapping and [../HARNESS-RESEARCH.md](../HARNESS-RESEARCH.md) for explicitly sequenced post-shell research.
