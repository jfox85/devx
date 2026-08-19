# DevX Operator Console v3

A static, interactive shell study for operating a high-density DevX session fleet without replacing the terminal. V3 responds directly to fleet-density, output-reader, action-hierarchy, and ArtifactPane feedback while retaining the existing blue/cyan product action system and green semantic state.

## V3 rationale

- **The navigator is designed for a large fleet.** Desktop session rows are one-line and 32px high, closely matching production density. The 25 complete fixtures demonstrate four project groups and allow roughly 20 sessions to remain visible in a 1000px viewport. Every row keeps separate textual/accessibility status and user-color indicators, name, target, and artifact count. Branch and detailed status reasons move to the row tooltip, selected-session header/facts, and Status panel rather than making every row two lines.
- **Touch density remains safe.** Coarse-pointer and phone rows become 44px high. Project grouping, collapse, filtering, arrow/Enter navigation, stale review, and quick switching continue to work.
- **Branding is literal.** The top-left wordmark is the full lowercase `devx`, never an abbreviated glyph.
- **Output is a reader, not a small dialog.** Output opens a true full-viewport surface modeled after production `PaneViewerModal`, with session identity, Open Tab, Close, readable scrolling output, focus entry/trapping/restoration, and Escape dismissal.
- **Actions are named and stable.** Desktop shows the fixed sequence **Output · Artifacts · Split: current mode · Compose · More**. Tablet and mobile replace that set with one clearly labeled **Actions** control and descriptive menu. New Artifact and Insert Reference live inside the artifact workspace rather than appearing as mystery glyphs.
- **ArtifactPane parity is restored within a static prototype.** The session-scoped pane provides folder grouping; newest/oldest/title sorting; show/hide list; draggable list resizing; image, text, JSX, video, HTML, PDF/iframe, and no-preview states; paste/drop/upload intake; new format/retention/tags; edit summary/tags/retention; Full Screen/Exit Full; refresh; close; and production-like Insert/Edit/Archive/confirmed Remove actions. Tablet/mobile use a distinctly labeled **Artifact actions** menu when labels no longer fit.
- **Terminal remains dominant.** Status stays closed by default. Below 1181px Status and Artifacts are mutually exclusive; phone layouts keep them as focus-managed destinations.

## Interactions to try

- Filter the 25 sessions; use `/` to focus filtering and arrow keys/Enter to navigate.
- Press `Cmd/Ctrl+P` for the complete quick switcher and `Cmd/Ctrl+K` for compose.
- Hover or focus the selected compact row to disclose branch/reason details, or open **Status** for the full explanation.
- Open **Output**, scroll its full-viewport transcript, use **Open Tab**, and press Escape to restore focus.
- Open **Artifacts**, change sort order, collapse/resize the list, select an image/text/JSX item, switch JSX between **Preview** and **Code**, and exercise Insert/Edit/Archive/Remove.
- Use **Full Screen** inside Artifacts, then Exit Full or Escape. Switch sessions to see scoped artifacts update together with identity, facts, terminal, routes, and status.
- Cycle Split through **terminal → vertical → horizontal → artifacts → terminal**.
- At tablet/mobile widths, open the labeled **Actions** control and cycle every Split mode. On phones, Artifact and Status destinations remove terminal-only action/composer chrome; Artifacts has its own distinctly labeled action menu.
- Use mobile compose, paste-only/send, and the soft-keybar. Open stale review and the UI state gallery for retained regression states.

## Accessibility and responsive notes

- Operational text remains compact while muted contrast is raised; meaningful state is never color-only.
- Controls expose `aria-expanded`, `aria-current`, `aria-selected`, `aria-modal`, descriptive labels, and native selection semantics where applicable.
- The full-screen Output reader moves focus to its heading, makes the shell inert, traps Tab, supports Escape, and restores the invoking control.
- The closed compact navigator is inert and accessibility-hidden. Open state focuses search, traps Tab, isolates the workspace, and restores its trigger.
- Status, Artifacts, and their full-screen states preserve panel exclusivity and focus restoration. Full-screen Artifacts is an `aria-modal` dialog surface, isolates the shell, traps forward/reverse focus, supports Escape, and returns focus. Coarse pointers receive 44px session rows and controls.
- The shell is viewport-bound with internal navigator, terminal, panel, preview, reader, and dialog scrolling.

## Truthfulness boundaries

This prototype does **not** claim live session runtime, age, ahead/behind count, exact changed-file breakdown, or route health. Route presence is an address, not a health check. User color remains an identifier distinct from semantic status and selection.

All 25 initial rows have complete fixture objects. Session selection re-renders identity, facts, terminal, status, routes, artifacts, Gatepost placement, and artifact preview together. Local create/rename/color/artifact transitions update the fixture; unavailable backend execution remains explicitly described as prototype placement.

No production files are changed. API calls, SSE, ttyd iframe behavior, persisted upload, real media/PDF payload rendering, cleanup, and token execution remain production responsibilities. Local file intake uses browser `FileReader` only and remains in-memory.

Run `python3 design-prototypes/operator-console/validate.py` while serving this directory at `http://127.0.0.1:4181/`. Coverage includes 1440×1000, 1280×900, 1100×800, 1024×768, 900×800, 768×1024, 700×800, 601×800, 540×760, 390×844, 360×780, and 320×700 with no document overflow or console/page errors.

See [UI-INVENTORY.md](./UI-INVENTORY.md) for the production capability mapping.
