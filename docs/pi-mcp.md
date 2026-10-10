# Managed Pi agents over MCP

`devx mcp pi` is a local, stdio-only MCP server that lets an MCP client
start Pi coding agents in new DevX sessions, send them follow-ups, and read
their results. **Every managed agent is an ordinary interactive Pi TUI in a
DevX tmux window.** A human can watch it, attach, and take control at any time.
Taking control stops remote prompts from being delivered until the human
releases it.

## Why this design

Before choosing a design I checked what the installed Pi (1.0.4) actually
supports:

| Option | What it would mean | Outcome |
|---|---|---|
| `pi --mode rpc` (headless) | A separate process that owns the conversation | Rejected. RPC and the TUI are separate processes. If one session file is open in both, they diverge. A human could not drop into the *live* agent. |
| tmux `send-keys` into the TUI | Type prompts the way a human would | Rejected. Delivery can't be verified, prompts can land in a shell or in a human's half-typed text, and there is no fence against a human typing at the same moment. |
| **Bridge extension inside the TUI** (chosen) | `pi -e devx-bridge.ts` in the interactive TUI, using `pi.sendUserMessage`, the `input` event (`source: interactive \| extension`), `agent_end`/`agent_settled`, `ctx.abort()` and `ctx.ui.getEditorText()` | One live process and one conversation. Human input is detected exactly, and delivery is confirmed. Proven with a real Pi (see Evidence). |

Before building on these APIs, I checked their behavior against a real
interactive Pi. The interactive TUI loads `-e` extensions.
`sendUserMessage` starts a visible turn. Keystrokes typed in tmux arrive as
`input` events with `source: "interactive"`. Aborting finishes with
`stopReason: "aborted"`. A process killed with SIGKILL can be resumed with
`--session-id`, and the earlier history is restored.

## Architecture

```
MCP client ──stdio──▶ devx mcp pi ──▶ piagent.Manager ──▶ ~/.config/devx/pi-agents/
                                                        agents/<pa_id>/{agent.json,tasks/,results/,events.jsonl,bridge.json}
                                                                   ▲ shared mkdir lock (agent.lock)
DevX session "<name>"  tmux window "pi-agent"                      │
   └─ pi --session-id <uuid> -e devx-bridge.ts  ◀── bridge: delivers / reports ──┘
         ▲ human: devx agent attach | devx session attach | DevX web terminal
```

- **IDs.** Agents use `pa_<12 hex>` and tasks use `pt_<16 hex>`. The Pi
  session ID is a UUIDv7 that DevX assigns and passes as `--session-id`. All
  three are written to disk before any side effect happens.
- **Idempotency.** `pi_start_task` and `pi_send` require an
  `idempotency_key`. A retry with the same key returns the original IDs and
  finishes any steps a crash interrupted. Reusing a key with a different
  request is an error.
- **Delivery.** The bridge checks the queue every 250 ms. It delivers the
  oldest waiting task only when all of these hold:
  - DevX holds the lease
  - Pi is idle and has no queued messages
  - the editor is empty (a human isn't typing)
  - this launch's nonce is current
  - the bridge runs in the bound pane

  It marks the task `running` under the lock, then calls `sendUserMessage`.
  The `input` handler confirms delivery under the lock. If a human took
  control in between, the handler returns `handled`, which drops the
  injection, and puts the task back in `waiting`.
- **Lease and fencing.** `agent.json.lease` is `{holder: managed|human,
  generation}`. These events move control to the human:
  - `devx agent takeover`
  - `/devx-takeover` in Pi
  - submitting any message in the TUI
  - pressing Escape on a remote turn

  Only a human can release control, with `devx agent release` or
  `/devx-release`. There is no MCP release tool. A relaunch rotates the
  launch nonce, so a stale Pi process is fenced out.
- **Task states.**
  - `waiting`, with a `waiting_reason`: `human_control`, `busy`,
    `queued_behind_other_task`, `bridge_starting_or_offline`,
    `pane_binding_stale` or `pending_delivery`
  - `running`, `completed`, `failed` or `cancelled`
  - `unknown`: the task was delivered, but the Pi process that held it is
    gone. Its outcome can't be known, so it is never retried automatically.
- **Agent states.** `idle`, `running`, `human_control`, `unknown` (stale
  heartbeat), `binding_stale` (the pane now belongs to another window),
  `pane_exited` or `not_launched`.
- **Restart and reconnect.** Nothing lives only in memory. Status reconciles
  disk state against tmux (`display-message -t %pane`, exact ID echo) and the
  bridge heartbeat (age, PID liveness, nonce, pane).
- **Events.** `events.jsonl` is append-only, with monotonically increasing
  `seq` values written under the shared lock. `pi_events(after_seq)` replays
  exactly what a client missed. `wait_seconds` (30 s max) blocks on the local
  log; nothing ever polls a model. This replaces the memory-only SSE for this
  feature; the existing SSE is unchanged.
- **Bounded output.**
  - prompts: 64 KiB max
  - status excerpts: 4 KiB, taken from the end of the text
  - `pi_result` chunks: 16 KiB, addressed by a UTF-8-safe byte cursor
  - event pages: 200 events max

  Results, errors and event strings pass through credential redaction. This
  is defense in depth, not a guarantee.

## Safety boundaries

- **Transport:** stdio only, with no network listener.
- **Tools:** a fixed allowlist of `pi_list_sessions`, `pi_start_task`,
  `pi_send`, `pi_status`, `pi_result`, `pi_events` and `pi_cancel`.
  Unknown tool names and unknown arguments are rejected. There is no
  shell/exec tool and no takeover or release tool.
- **Starting agents:** `pi_start_task` only works for projects listed in
  `pi_mcp.allowed_projects`, and only creates a **new** session. If the
  session, its branch, its worktree path or a same-named tmux session
  already exists, the call is denied. MCP cannot attach to or control
  sessions that DevX didn't create through this interface.
- **Local-only sessions:** managed sessions are created in local-only mode,
  not through `devx session create`. The record carries a `local_only`
  marker bound to the agent id. The session gets:
  - no service ports or routes
  - no `.envrc`, `.tmuxp.yaml` or bootstrap files
  - no project template windows (no editor Pi, no services)
  - no Caddy/Cloudflare sync

  Its tmux session has one inert `devx-local` window plus the `pi-agent`
  window. Caddy, the Cloudflare tunnel, the health check and the web API
  skip any marked session, even if ports or routes are later added to its
  record by hand. `devx session rm` on it does no shared route sync and runs
  no project cleanup command. A missing or foreign marker is a permission
  denial; nothing is adopted.
- **Cancellation:** `pi_cancel` on running work is denied while a human holds
  control.
- **Prompt delivery:** prompts are never typed into tmux. The launch script
  holds the pane with `tail -f /dev/null` after Pi exits, so there is never a
  shell in that pane.
- **Bridge scope:** the bridge is written to the DevX state dir and loaded
  with `-e` only for managed agents. It is never installed globally.
- **Out of scope:** push, PRs, merges, deployment and credential handling.
  Redline's quota scheduler is not involved.

## Configuration

```yaml
# ~/.config/devx/config.yaml
pi_mcp:
  allowed_projects: [devx]        # required; empty denies every start
  pi_command: pi                  # optional
  pi_args: []                     # optional, e.g. ["--model", "..."]
  pass_env: []                    # optional env var names copied into the launch script
```

MCP client config: `{"mcpServers": {"devx-pi": {"command": "devx", "args": ["mcp", "pi"]}}}`

## Artifact bridge

Remote clients can exchange explicitly registered files with a managed
agent's session through three extra tools. The policy lives in the owner's
global DevX config (`~/.config/devx/config.yaml`, or the file given with
`--config`):

```yaml
pi_mcp:
  allowed_projects: [...]        # shared with pi_start_task
  artifacts:                     # optional block; every key optional
    read: true                   # default true: devx_artifact_list + devx_artifact_read
    upload: false                # default false: devx_attachment_upload
    sessions: [name, ...]        # optional restrictive allowlist (present, even [], = only these)
    exclude_sessions: [name]     # optional, always subtracted
    exclude_projects: [alias]    # optional, always subtracted
    max_upload_bytes: 10485760   # default 10 MiB, ceiling 25 MiB
```

**Defaults.**
- **Read is on.** If `sessions` is absent, every session available through
  the MCP path in an allowed project is eligible, minus the exclusions (see
  "Session eligibility" below).
- **An explicit `sessions` list restricts.** Only the listed sessions are
  eligible. An empty list exposes nothing. Exclusions still apply.
- **Upload is off.** Even with `upload: true`, uploads need an explicit
  `sessions` list naming the session. Uploads never use the default-wide
  read scope.
- **No allowed projects, nothing eligible.** With no `pi_mcp` block or an
  empty `allowed_projects`, nothing is eligible.

**Session eligibility.** One predicate decides whether an agent is the
unambiguous owner of a live session. It is re-evaluated on every call, for
the default scope and explicit lists alike:
- the agent record exists, is not retired, and has a project and session;
- the DevX session record with that name exists, its absolute path equals
  the agent's worktree, and its project (if recorded) equals the agent's;
- it is a host session (not a container target);
- an ownership marker, if present, names this agent: `local_only.agent_id`
  for sessions created by `pi_start_task`, `managed_agent` for adopted
  sessions. Both markers at once, or either naming another agent, is a
  conflict and always denies; no policy setting overrides it;
- **session instance:** every DevX session record has a random,
  stable `instance_id`, assigned when DevX creates the record and never
  changed by later saves. Removing a session and creating one with the
  same name and path gives a different id. Every agent records the
  instance it was bound to (`session_instance_id`, set by `pi_start_task`,
  by adoption, or by the reviewed migration below):
  - a bound agent is eligible only while its session record carries that
    same id. A recreated session never matches, whatever its name, path,
    timestamps or markers, even if the clock was rolled back or an old
    record was restored over it;
  - if an older DevX binary rewrote the record and dropped the field, the
    record's `created_at` must equal, to the nanosecond, the
    `session_created_at` the agent recorded at binding. Old writers keep
    `created_at`; a recreated record gets a new one. The migration
    restores the dropped id;
  - an agent from before instance ids that hasn't been migrated is
    eligible only while a marker on its session names it, the session
    record is no newer than the agent, and the record has no instance id.
    A marker alone isn't proof: before instance ids, relaunching an
    adopted agent re-marked whatever session had the name. Otherwise it
    stays denied until the owner binds it with the reviewed migration;
- no other non-retired agent claims the same session, and no other live
  agent or other session record uses the same worktree.

Relaunch and adoption replay only **verify** the binding: they never
write a marker or rebind. A bound adopted agent verifies by instance,
even if an older writer dropped its marker. An unbound legacy agent needs
its marker until it's migrated. An agent whose session was recreated must be
adopted again deliberately (which creates a new agent) or retired.
`devx session rm` retires the marker's agent and every agent bound to
that exact instance, even if an older writer dropped the marker.
Sessions removed with `devx session clear` leave their agents unretired,
but those agents can never match a later session with the same name.

**Migration: `devx session instances`.** Records created before
instance ids have none. The migration assigns them and binds each agent
to its session, but only when the evidence is unambiguous:
- **Dry run by default.** It prints the plan and writes nothing.
  `--prepare` creates only a private random key, `.instance-key` (0600,
  next to `sessions.json`). Ids are derived from the record state with
  that key, so the plan hash stays the same until a record changes.
- **Binds only clear cases.** An agent is bound only if all of these
  hold; anything else is listed with its reason and left unchanged:
  - its session exists at the agent's worktree with the same project;
  - no marker names another agent;
  - there's no duplicate claimant or shared worktree;
  - the session record was created no later than the agent, whatever the
    marker;
  - the record carries no instance id the migration didn't derive itself.
- **Restores dropped bindings.** If an older binary rewrote an agent's
  `agent.json` and dropped its binding, the plan restores it from the
  agent's append-only event log (`restore_agent_binding`), but only if
  the session is still that exact instance. It never makes a new binding.
  Old writers can also drop a session's id; the plan restores the id the
  agent is bound to.
- **Apply.** `--apply <plan-hash>` re-plans and refuses unless the hash
  matches. It then backs up `sessions.json`, every agent record it
  touches and the plan to `~/.config/devx/backups/session-instances-*`.
  It journals each write before making it and re-checks every step
  against the latest records under their locks; anything changed since
  the review aborts with "records changed".
- **Skipped agents lose legacy access.** A marked legacy agent that the
  migration skips loses bridge access once its session gets an id: the
  legacy rule requires an id-less record. That's intended (fail closed).
  Resolve skipped agents before applying, or adopt them again afterwards.
- **Repeatable.** Every step skips work that is already done. After an
  interrupted apply, run the dry run again and apply the new (smaller)
  plan.
- **Rollback.** `--rollback <backup-dir>` removes exactly the values the
  journal records, and only where the record still holds them. Ids that
  restored an agent's existing binding are kept. Roll back the newest
  run first: a later run may have written identical values, so rolling
  back an older run needs `--force-older` (runs already rolled back don't
  count). Every apply gets its own backup directory.
- **Writes only these fields.** Only `instance_id` on sessions and
  `session_instance_id` / `session_created_at` on agents are written,
  plus an agent event. Worktrees, tmux sessions, Pi conversations,
  leases and markers are never touched. Output contains names, ids,
  reasons and timing differences, never file contents or secrets.

**Old writers.** A `devx` binary from before this change can rewrite
`sessions.json` and drop `instance_id`, or rewrite `agent.json` and drop
`session_instance_id` / `session_created_at`. Examples are a
long-running `devx web` and old relay `devx mcp pi` children. Neither
grants access:
- a session without its id is matched only by the exact created_at;
- an agent without its binding falls back to the strict legacy rule
  until the migration restores the binding from its event log.
Run the dry run again after upgrading every DevX process; it restores
dropped ids. Upgrade or restart old DevX processes before the
migration so they stop dropping fields.

`pi_list_sessions` is an **inventory**, not an eligibility list. It returns
every agent record, including retired agents and agents whose session was
removed, moved, or is claimed by another agent, plus `allowed_projects` for
information only. The bridge never widens to those records; only agents
passing the predicate above and the owner policy are eligible.

**Reload, no restart (once running a build with reload).** The policy is
re-read and validated on every `tools/list` and every tool call, and the
agent and session records are re-read on every call. Once a `devx mcp pi`
process from a reload-capable build is running, changes apply on the next
call without restarting it or the Agent Shed relay. That includes:
- enabling or disabling read or upload;
- adding or removing sessions and exclusions;
- changing `allowed_projects`;
- retiring agents and removing sessions.

Revoking access between two chunks of a read stops the next chunk.
Exceptions:
- **Gateway approval.** Turning a capability on adds tool definitions,
  which the Agent Shed gateway (and the relay's `allowed_tools`) must
  separately allow and approve. Tool definitions are fixed text and never
  depend on policy values, so changing session scope never invalidates an
  approved definition.
- **New binary.** Installing a new `devx` binary does not change a
  `devx mcp pi` process that is already running. The relay keeps reusing
  its existing child, and its idle eviction only happens when it starts
  another child, so it is not a reliable way to switch. After installing,
  start exactly one fresh child (restart the relay, or end only its
  `devx mcp pi` child), then confirm that the new child started after the
  install before relying on live reload. After that, scope edits reload
  dynamically.
- **Separate settings.** `pi_start_task`/`pi_send` settings (`pi_command`,
  `pi_args`, and so on) are still read once at process start. That includes
  the `allowed_projects` value that `pi_start_task` enforces and
  `pi_list_sessions` reports. Until the child restarts, these can differ
  from the bridge's live value.

**Fails closed.** If the config can't be read or doesn't validate, the
bridge lists no tools and answers every bridge call with `unavailable`
until the file is fixed. These are all errors, never a fallback to
defaults:
- an unknown key under `pi_mcp.artifacts` (for example, `session:`);
- a wrong type, or null;
- a duplicate key;
- a YAML alias or merge key;
- unparseable YAML;
- an oversized file, or a FIFO or other non-regular file;
- a file not owned by the current user, or writable by group or others
  (another local user could otherwise widen the scope).

Project-level `.devx/config.yaml` files and `DEVX_*` environment variables
never affect the bridge policy.

**Migration from 39e4a9a.** In 39e4a9a, read and upload defaulted to off
and `sessions` was required. Now:
- **Configs with an explicit `sessions` list keep exactly their scope.**
  The fixture-only live config behaves identically.
- **Configs with no `artifacts` block** now get read access across all
  sessions available through MCP in allowed projects, including marker-less
  legacy and adopted sessions. To keep the bridge off, set `read: false` or
  `sessions: []` before upgrading.
- **`read: false` or `upload: false`** behave as before.
- **Exclusions and live reload** (`exclude_sessions`, `exclude_projects`)
  are new.

**Trust boundary.** `devx mcp pi` receives no caller identity. The Agent
Shed gateway authenticates the principal and checks that principal's
reviewed per-tool grant on the `devx-pi` relay app, then forwards only the
tool name and arguments. The `agent_id` argument is chosen by the caller.
DevX cannot tell whether a particular caller may see a particular session.
It enforces only the owner's policy:
- Any principal granted a bridge tool can use it on every eligible
  session, and on no others.
- Under the default-wide scope, "eligible" means every session available
  through MCP in every allowed project (see "Session eligibility").

Per-principal or per-session separation would need the gateway to pass a
verified principal (or a per-binding scope) to the relay, and that doesn't
exist today. Grant the bridge tools only to principals that may see every
eligible session.

On every call DevX also checks the session eligibility predicate and that
the project is allowlisted and not excluded.

**Eligible files.** Only files registered in the session's existing artifact
manifest (`<worktree>/.artifacts/manifest.json`, the same store as
`devx artifact add/list` and the web artifact pane). There is no parallel
registry and no general path argument anywhere. Uploads go only to
`.artifacts/attachments/` and are registered in the same manifest with tag
`remote-attachment` and agent `remote-upload`.

**Scope checks on every call.**
- The `agent_id` must exist and not be retired.
- Its project must be in `pi_mcp.allowed_projects`.
- Its DevX session must still point at the agent's worktree, with the same
  project, and must not be owned by another agent (local-only marker or
  adoption).

Unknown and unauthorized agents get the same `permission_denied`. Artifact
IDs (`dxa_<24 hex>`) are derived from agent + session + manifest id, so an
ID from one session is `not_found` in every other.

**Reading.** `devx_artifact_list` returns:
- the opaque id;
- title, type and MIME type (from the extension);
- size and retention;
- content `version` (`c_` + 24 hex of SHA-256) and `checksums.sha256`.

`devx_artifact_read` returns one chunk:
- text in UTF-8-safe chunks of at most 16 KiB (`next_offset`/`eof`);
- PNG/JPEG/GIF/WebP files of 40 KiB or less, starting at offset 0, as an MCP
  `image` content block (bytes must sniff as the declared type; SVG never
  does);
- anything else as base64 chunks in `structuredContent.data_base64`.

Every chunk carries `chunk_sha256` and the full-file checksum. Every result
stays below 60,000 bytes because the Agent Shed gateway caps results at
64 KiB.

**Version semantics.** DevX artifacts are mutable files with a stable
manifest ID and no content history. A read may pin `version`. If the file
changed since, the read fails with `version_mismatch` and reports
`current_version`, so chunks of different contents are never mixed.
Earlier contents are not retrievable. Each read hashes the file and slices
the chunk in the same pass, with `fstat` before and after; a concurrent
change returns `changed_during_read`. Checksums are cached in-process by
path, device, inode, ctime, size and mtime. Any write changes ctime, so
cached data can't describe changed bytes. A read that hits the cache reads
only its window with `pread` and requires an identical `fstat` before and
after. A full 32 MiB download takes 2,048 calls of about 1.5 ms each
locally (measured); a cold hash takes about 25 ms.

Text chunks are shortened on a character boundary so the JSON-escaped text
(sent twice: in the content block and `structuredContent.text`) stays
within budget. Text made entirely of escape-heavy characters (`<`, control
characters) yields about 4,500 bytes per call; ordinary text yields 16 KiB.
Images larger than 40 KiB aren't returned inline. Read them with
`encoding: base64` (a 300 KB screenshot takes 19 calls). Nothing is ever
resized or substituted.

**Path safety.** The manifest and every file are opened component by
component with `openat(O_NOFOLLOW)` from the worktree, so a symlink
anywhere fails, including `.artifacts`, `manifest.json`, or a directory
swapped in after validation. A race test flips a parent directory to an
outside symlink thousands of times and never reads outside content.
Uploads are published the same way:
- the attachments directory is opened without following links;
- bytes go to an `O_EXCL|O_NOFOLLOW` temp file;
- the temp file is published with `linkat`, which fails instead of
  replacing an existing name, so the next free `name-N.ext` is used;
- the manifest entry is then added through the same no-follow chain: one
  descriptor for `.artifacts`, the shared `.manifest.lock` flock (the same
  lock `devx artifact add` takes), the manifest read with
  `openat(O_NOFOLLOW)`, and the new manifest written to an
  `O_EXCL|O_NOFOLLOW` temp file and swapped in with `renameat`. Swapping
  `.artifacts` for a symlink at any point cannot redirect any of these
  writes. If registration fails, the just-published file is removed.
The leaf must also be:
- a regular file with one link (no FIFOs, devices or hard links to files
  elsewhere);
- at most 32 MiB.

Error messages contain no absolute paths or file contents. On platforms
without `openat`/`O_NOFOLLOW`/`linkat` (Windows), both capabilities are
forced off and the bridge fails closed.

**Uploads.** `devx_attachment_upload`:
- **Chunking.** Each call carries at most 8 KiB, keeping base64 arguments
  under the gateway's 16 KiB argument bound. Every chunk repeats the same
  `idempotency_key`, `filename`, `mime_type`, `size` and `sha256`, plus its
  `offset`.
- **Offsets.** Chunks are accepted only at the current received offset. A
  repeated chunk with identical bytes is acknowledged. A call without
  `data_base64` reports `next_offset`, so an interrupted client can resume.
- **Staging.** Partial bytes are staged under
  `~/.config/devx/pi-agents/artifact-bridge/uploads/<agent>/`, never inside
  a worktree.
- **Limits.**
  - At most 8 incomplete uploads per agent. Staged bytes with no progress
    for 24 hours are dropped.
  - Per session, at most 200 remote attachments and 512 MiB of them in
    total (registered entries tagged `remote-attachment`, measured on
    disk). The quota is checked under the manifest lock; when it's full,
    uploads fail with `limit` until attachments are removed with
    `devx artifact rm`.
  - Completion records (which let a finished upload be replayed) are kept
    for 30 days.
- **Verification.** When the last byte arrives, the declared size and
  SHA-256 are checked, then the content: PNG/JPEG/GIF/WebP/PDF magic bytes,
  UTF-8 with no NULs for text/Markdown/CSV, and valid JSON for JSON. Only
  then is the file published and registered (see "Path safety"). Publishing
  never overwrites: a name collision gets a `-2` suffix. No "assets" are
  recorded from uploaded Markdown. If any check fails, the staged bytes are
  discarded. If the stored file fails read-back verification, the upload
  attempt is discarded (retry with a new key) and the registered entry
  stays visible with its actual checksum.
- **Allowed types.** HTML, SVG, scripts and archives are refused.
- **Idempotency.** Reusing a key with different metadata is `conflict`.
  After completion, the same call returns the same artifact
  (`replayed: true`).
- **Response.** It includes `local.manifest_id`; the local Pi agent resolves
  the file with `devx artifact url <manifest_id> --local` or reads
  `local.path` in its worktree.

**Task results.** When reading is enabled, `pi_status` adds `artifacts`:
entries registered in the agent's session between delivery and finish
(+30 s), excluding remote attachments, at most 20. The field is omitted
otherwise, so existing clients are unaffected.

**Retention.** Attachments and artifacts follow normal DevX artifact
retention. `session` entries go with the session's worktree. `devx artifact
archive <manifest-id>` copies them to the project archive on session
removal. Nothing in the bridge deletes registered artifacts. Agent Library
publication is a separate, optional, human step.

**Not provided.**
- download URLs or public links;
- remote URL fetches;
- rendering of HTML/SVG;
- MCP `resources/*` (the Agent Shed relay carries `tools/*` only).

## Human controls

| Action | How |
|---|---|
| See all managed agents | `devx agent list` (also visible as normal sessions in DevX TUI/web) |
| Inspect without touching | `devx agent inspect <id>` (pane snapshot), `devx agent status <id>` |
| Drop in | `devx agent attach <id>`, `devx session attach <session>`, or the DevX web terminal → window `pi-agent` |
| Take control | `devx agent takeover <id>`, `/devx-takeover`, or just type a message / press Esc in Pi |
| Give control back | `devx agent release <id> [--drop-queued]` or `/devx-release` |
| Recover a crashed Pi | `devx agent relaunch <id>` (same Pi session ID, history restored) |

Inspecting is read-only. Attaching only lets you watch until you type
something. Taking control fences out remote prompts.

## Limitations

- **Host sessions only.** Docker and Gatepost targets are rejected when an
  agent starts.
- **The bridge must load.** If Pi starts without it (for example, a
  user-level setting disables `-e`), the agent shows up as `unknown`, its
  tasks stay `waiting`, and nothing is injected.
- **Racing a human submit.** If a human submits text in the moment between
  the bridge's idle check and its `sendUserMessage`, Pi runs input handlers
  in call order. If the human's input lands first, the lease moves to the
  human and the remote prompt is fenced (`delivery_fenced`). If the remote
  prompt lands first, it is confirmed, and the human's message is queued
  behind it as a normal Pi follow-up. The explicit-takeover race is
  exercised by a real-Pi test. For the human-submit race, the human-first
  order is tested deterministically (`TestE2EHumanEnterBeatsPendingRemoteSend`);
  the remote-first order is reasoned from Pi's ordering.
- **Mid-turn takeover doesn't stop the current turn.** A task that already
  reached Pi keeps running. The human sees it and can press Esc.
- **Redaction is pattern-based.** It catches common credential formats only.
- **Dispatch needs a live Pi TUI.** Prompts are delivered only while Pi is
  running in the pane. If Pi exits, the agent shows as `pane_exited` until
  `devx agent relaunch`.

## Test isolation (tmux)

Tests that start tmux must use `internal/tmuxfixture`:

- **Private socket.** Every command runs as `tmux -S <fixture-dir>/tmux.sock`.
  The fixture dir is created by the fixture itself and recorded in
  `owner.json` with a random nonce. The child environment has `TMUX`,
  `TMUX_PANE` and `TMUX_TMPDIR` removed.
- **Rejected before execution:**
  - global options from callers (`-L`, `-S`, `-f`, …)
  - `;` command chaining
  - `kill-server`, `start-server` and `source-file`
  - missing, relative, default, inherited (`$TMUX`) or unowned sockets
  - a tampered ownership record
- **Cleanup scope.** Cleanup only runs `kill-session -t =<name>` for
  sessions recorded through `NewSession`. It never kills a server. Every
  executed argv is written to `commands.log`, and the piagent E2E harness
  asserts on that log.
- **Package guard (`cmd`, `session`, `web`).** `TestMain` calls
  `tmuxfixture.RunGuarded`. That installs a test-only `tmux` wrapper first
  on `PATH`, which is the test binary re-executed in wrapper mode.
  - **Every real exec is pinned.** Each tmux exec reached by these tests,
    whether direct, through production code or through tmuxp/libtmux, runs
    as `<abs real tmux> -S /tmp/dxtw-*/tmux.sock …`. `TMUX`, `TMUX_PANE`
    and `TMUX_TMPDIR` are removed, and ownership is verified first.
  - **Same refusals as the fixture.** `kill-server` and the other refused
    commands, caller `-L`/`-S`/`-f`, `;` chaining, and default, missing or
    unowned sockets are all rejected before exec.
  - **Fake HOME.** `HOME`/`XDG_*` point at a fake test store, so
    `sessions.json` side effects never touch real metadata.
  - **Cleanup.** After the tests, sessions on the owned socket are killed
    by exact name. The server is never killed.
  - **Former opt-in removed.** The previously gated legacy tests run under
    the wrapper. The `DEVX_ALLOW_UNSCOPED_TMUX_TESTS` opt-in is gone.
