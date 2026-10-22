# Onboarding audit fixes

Before: `4b009c8` (`origin/main` when this work began).
After: the changes in this pull request. macOS arm64, isolated HOME and minimal
system PATH, Caddy disabled. The missing origin warning is from the local-only
Git fixture, not a product regression.

These are browser captures of **actual CLI transcripts** and **README source
excerpts**, rendered for comparison. They are not screenshots of the DevX web UI;
this change does not modify that UI. Temporary filesystem paths are normalized to
`<audit-sandbox>`; the commands, errors, and statuses are captured from execution.

| Surface | Before | After |
| --- | --- | --- |
| Config creation, dependency check, terminal recovery | [CLI before](cli-before.png) | [CLI after](cli-after.png) |
| Release/version and dependency guidance | [Docs before](docs-before.png) | [Docs after](docs-after.png) |

Regression coverage includes fresh and partial config initialization, preservation
of existing config/template contents, explicit destinations, invalid destinations,
dependency classification, failed terminal handoff recovery, and version-matched
installer documentation for latest and pinned versions. `scripts/check-onboarding.sh`
checks the built binary before publishing a release and on the Linux build artifact.

The published v0.3.0 binary remains unchanged. The README now directs release users
to its tagged documentation and labels features requiring a development build;
publishing v0.4.0 is outside this PR.
