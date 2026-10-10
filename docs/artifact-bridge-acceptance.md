# Artifact bridge: live acceptance test (prepared, NOT executed)

This checks the artifact bridge end to end through the real chain:
Jarvis (ChatGPT) → Agent Shed gateway → Agent Shed relay → `devx mcp pi`.

Nothing here has been run against the live gateway or relay. Each step that
changes live permissions, configuration or running services is marked
**OWNER**. Those steps need explicit owner approval and must be done by the
owner.

## What is already proven locally

- `go test ./artifactbridge` (unit and MCP server level) and
  `go test -run TestArtifactBridgeBinaryEndToEnd ./cmd` (real `devx`
  binary over stdio, fake HOME). Together they cover:
  - capability gating from config;
  - list with checksums and versions;
  - exact readback of PNG and Markdown, including chunking and UTF-8
    boundaries;
  - chunked upload with probe/resume, replay and conflict;
  - local resolution with `devx artifact url --local`;
  - cross-session, cross-project, traversal, symlink, FIFO and hard-link
    denial, plus size limits and malformed or mismatched MIME types;
  - every result line staying under 64 KiB.
- Source review of Agent Shed candidate `050f757`:
  - relay apps use `src/generic.ts`, which returns the upstream `callTool`
    result (including non-text content blocks) after a 64 KiB size check and
    a credential-echo check;
  - arguments are capped at 16 KiB;
  - only `tools/*` methods cross the relay.

  This is code reading, not an observed run.

## What is NOT proven

1. That the deployed gateway build passes MCP `image` content blocks
   through unchanged. The legacy `src/gateway.ts` path would reject them,
   because it accepts text content only.
2. That ChatGPT/Jarvis **renders** an MCP `image` block as an image. A
   successful tool call alone does not prove this; it has to be seen in the
   conversation UI.
3. That the relay's per-call deadline (25 s at the gateway, 60 s for
   `devx-pi`) is enough to hash large artifacts. Up to 32 MiB per file is
   allowed.
4. That the gateway's tool-definition review accepts the three new tools.
   Adding them changes the `devx-pi` tool catalog.

## OWNER steps (activation)

1. **Install the new build** of `devx` from this branch:
   `make install` in this worktree.
   - Effect: replaces `~/projects/go/bin/devx`, which the relay and DevX web
     both use.
   - Already-running `devx web` keeps running the old binary until it
     restarts.
   - An already-running `devx mcp pi` child of the relay also keeps the old
     binary. The relay must start one fresh child (see "Live reload" below)
     before the new build serves any call.
2. **Enable capabilities** in `~/.config/devx/config.yaml`. Start with read
   only, exposing just the synthetic fixture session:
   ```yaml
   pi_mcp:
     artifacts:
       read: true
       upload: false
       sessions: [artifact-bridge-fixture]   # nothing else is exposed
   ```
   Every principal granted these tools in the gateway can read every listed
   session. DevX receives no caller identity (see docs/pi-mcp.md, "Trust
   boundary").
3. **Allow the tools in the relay** (Agent Shed relay config,
   `services[id=devx-pi].allowed_tools`): add `devx_artifact_list` and
   `devx_artifact_read`. Add `devx_attachment_upload` only when upload is
   approved. Then apply the config the way the relay documents. That may
   restart the relay daemon (`agentshed-relay`), which is a shared service.
4. **Review and approve the changed tool definitions** for Jarvis in the
   Agent Shed dashboard. The gateway reports
   `tool_definition_changed_review_required` until this is done.

## Test script (run by Jarvis after the OWNER steps)

Use a dedicated test session with a known agent id (`<AGENT>`). Register a
small synthetic screenshot (under 40 KiB, so it is eligible for an inline
image block) and a Markdown report:

```bash
cd <that session's worktree>
screencapture -x -R0,0,320,200 ./acceptance-shot.png   # or any small PNG
printf '# Acceptance report\n\nSynthetic ✓\n' > ./acceptance-report.md
devx artifact add ./acceptance-shot.png --title "Acceptance screenshot"
devx artifact add ./acceptance-report.md --title "Acceptance report"
shasum -a 256 .artifacts/screenshots/acceptance-shot.png .artifacts/*/acceptance-report.md
```

Then, in Jarvis:

1. `devx_artifact_list {agent_id: <AGENT>}`
   - Expect both entries with `mime_type`, `size`, `version` and
     `checksums.sha256` equal to the local `shasum`.
2. `devx_artifact_read {agent_id, artifact_id: <screenshot id>, version}`
   - **Transport check:** the result has `content[0].type == "image"`.
   - **Rendering check:** a person confirms the screenshot is visible in
     ChatGPT. Record the result as one of: rendered / shown as attachment /
     not shown.
3. `devx_artifact_read` the report in 16 KiB chunks until `eof`.
   - Jarvis computes the SHA-256 of the concatenated text. It must equal
     `checksums.sha256`.
4. Upload (only after upload is approved):
   - Jarvis uploads a synthetic reference file in chunks of 8 KiB or less,
     with `idempotency_key: acceptance-ref-1`.
   - It repeats the last call and expects `replayed: true`.
   - Locally, `devx artifact url <local.manifest_id> --local` resolves the
     file, and its `shasum` equals the declared sha256.
5. Denials:
   - `devx_artifact_read` using an id from step 1 but a different agent's
     `agent_id` returns `not_found`.
   - `devx_artifact_list` for an agent in a non-allowlisted project returns
     `permission_denied`.
6. `pi_status` for a task during which the agent ran `devx artifact add`
   includes `artifacts[]` with that artifact's `dxa_` id.

Record the results (including the rendering observation) in an artifact.
Then roll back: set the capabilities back to false, or remove the tools from
`allowed_tools`, if the test is not meant to stay enabled.

## Live reload (later builds)

From the build after 39e4a9a on, the bridge re-reads `pi_mcp.artifacts` and
`pi_mcp.allowed_projects` on every call. The explicit
`sessions: [artifact-bridge-fixture]` list keeps the scope fixture-only.
Removing the list switches to the default-wide read scope; see
docs/pi-mcp.md, "Artifact bridge", for the defaults, the session
eligibility predicate, and the trust boundary.

Activation is one-time:

1. Install the new binary, keeping the previous one as a backup. Running
   `devx mcp pi` children are not changed by the install.
2. Start exactly one fresh relay child: restart the relay in its tmux pane,
   or, with owner approval, end only the relay's `devx mcp pi` child. Idle
   eviction is not a reliable substitute.
3. Verify that the new child started after the install. Then prove reload
   without a restart: add `exclude_sessions: [artifact-bridge-fixture]`,
   confirm the next read is `permission_denied`, remove it, and confirm the
   next read succeeds. Do not claim live reload before this passes.
4. From then on, scope edits reload dynamically. Every later binary install
   needs one fresh child again.

No relay `allowed_tools` change, gateway Sync, or Jarvis tool refresh is
needed: the tool definitions are identical.

## Session instance ids (later builds)

From the build that adds `instance_id`, eligibility is bound to the exact
session instance (see docs/pi-mcp.md, "Session eligibility" and
"Migration"). Agents from before this build are unbound: each stays
eligible only while a marker on its session names it.

Owner steps, in order. Each step is separately approved, and nothing here
runs automatically.

1. **Review the preview.** On a copy of the records, or read-only on the
   live ones: `devx session instances` (the dry run writes nothing).
2. **Install the new binary.** Keep the previous one as a backup. Then:
   - start one fresh relay child, as in "Live reload" above;
   - restart any long-running old DevX process (for example `devx web`)
     so it stops dropping new fields;
   - don't restart Pi or tmux sessions.
3. **Prepare.** `devx session instances --prepare` creates the private key
   and prints the applicable plan and its hash. Review every `skip` line.
4. **Apply.** `devx session instances --apply <hash>`. Note the printed
   backup directory.
   - Until the migration runs, an UNBOUND adopted agent whose marker an
     older writer dropped can't be relaunched. Relaunch only verifies; it
     never re-marks. In the preview of the live records, this applied to
     one agent (the adopted session for this branch). After the migration
     binds it, relaunch is verified by instance and doesn't need the
     marker. Running Pi processes aren't affected either way.
5. **Verify.** Run the dry run again; it should report nothing to apply.
   Then check eligibility with a read-only inventory.
6. **Roll back if needed.** `devx session instances --rollback <dir>`.
   Restoring the old binary afterwards is safe: older binaries ignore the
   new fields.
