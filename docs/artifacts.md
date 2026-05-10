# DevX Artifacts

DevX artifacts are session-scoped files stored under a worktree's `.artifacts/` directory and indexed in `.artifacts/manifest.json`. Use them for plans, reports, screenshots, logs, diffs, recordings, QA notes, and proof-of-work outputs that should be visible in DevX Web.

## Add artifacts

```bash
# Flat artifact: .artifacts/report.md
devx artifact add ./report.md \
  --title "Completion report" \
  --type report \
  --agent pi

# Artifact from stdin: .artifacts/notes.md
cat notes.md | devx artifact add - \
  --file notes.md \
  --title "Notes" \
  --type document
```

## Foldered artifacts

Use `--folder` to organize related outputs into a collection. The folder is a safe relative path under `.artifacts/`.

```bash
RUN_ID="2026-05-09T120000Z"

devx artifact add ./00-office-hours.md \
  --title "Office hours notes" \
  --type document \
  --folder "workflow/$RUN_ID" \
  --file 00-office-hours.md

devx artifact add ./10-plan.md \
  --title "Implementation plan" \
  --type plan \
  --folder "workflow/$RUN_ID" \
  --file 10-plan.md

devx artifact add ./40-proof-of-work.html \
  --title "Proof of work" \
  --type report \
  --folder "workflow/$RUN_ID" \
  --file 40-proof-of-work.html \
  --focus
```

This creates paths such as:

```text
.artifacts/workflow/<run-id>/00-office-hours.md
.artifacts/workflow/<run-id>/10-plan.md
.artifacts/workflow/<run-id>/40-proof-of-work.html
```

Manifest entries include both:

- `file`: the full path relative to `.artifacts/`, for example `workflow/<run-id>/10-plan.md`
- `folder`: the grouping path, for example `workflow/<run-id>`

Older flat artifacts do not need a `folder` field and continue to load normally.

## Folder safety rules

Artifact folders must be safe relative paths:

- no absolute paths (`/tmp/report`, `C:\\tmp\\report`)
- no `..` or `.` segments
- no empty segments (`workflow//run`, `workflow/`)

Invalid folders are rejected before files are written.

## Listing and URLs

```bash
# All artifacts
devx artifact list

# Only artifacts in one folder
devx artifact list --folder workflow/<run-id>

# Grouped text output
devx artifact list --tree

# Machine-readable output still includes folder, file, path, and url
devx artifact list --json

# URLs and local references work with nested paths
devx artifact url <artifact-id>
devx artifact url <artifact-id> --local
devx artifact url <artifact-id> --embed
```

Example tree output:

```text
Unfiled/
  - report-completion [...] Completion report (report.md, session)
workflow/<run-id>/
  - plan-implementation-plan [...] Implementation plan (workflow/<run-id>/10-plan.md, session)
  - report-proof-of-work [...] Proof of work (workflow/<run-id>/40-proof-of-work.html, archive)
```

DevX Web groups artifacts by `folder`; artifacts with no folder appear under **Unfiled**. Focused artifact behavior is unchanged: `--focus` still opens the selected artifact in the web artifact pane.
