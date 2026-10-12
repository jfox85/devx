# Local-only ownership markers: preservation and backup-based repair

## Problem

An MCP-created (non-adopted) managed agent may only run in a session whose
`sessions.json` record carries a `local_only` marker naming it
(`localOnlySessionCreator.EnsureTmux`). If the marker is missing, relaunch is
denied, even though the agent's ownership is still witnessed elsewhere.

Every `sessions.json` mutation is a full read-modify-write. A DevX build whose
`session.Session` struct does not have `LocalOnly` / `InstanceID` (any build
of `origin/main` before #71/#72) decodes records into its older struct and
writes them back without these members, so **one write by such a build strips
the marker from every record**. `TestMainlineSchemaRoundtripDropsOwnershipMarker`
reproduces this with the struct copied verbatim from `origin/main` e22869d.

Diagnosis (read-only, from the incident) is consistent with this mechanism:
markers were present in two Oct 10 backups and absent afterwards for every
agent created before a point in time, and present for every agent created
after it. **Which process performed the stripping write is not proven**: there
is no writer audit log. Older-schema DevX binaries are the likely class of
writer; nothing here depends on identifying the exact one.

## Prevention (this change)

`session.SessionStore` (top level), `session.Session` (each record) and
`session.LocalOnlyMeta` (the marker object itself) now keep JSON members this
build does not know (`Unknown`, see `session/unknown_fields.go`) and write them
back unchanged. Other nested objects (`target`, `target.gatepost`, `review`)
are not covered yet. Known fields always win: keys are matched the way
encoding/json matches them. Fields DevX deliberately retired (currently only
`color`) are still dropped.

Limits (do not overstate):

- This protects fields written by **future** builds from being stripped by
  **this and later** builds.
- It cannot protect anything from binaries built **before** this change. An
  unmodified older binary keeps stripping `local_only`, `instance_id` and any
  other field it does not know. The only remedies are not running such
  binaries against the real `~/.config/devx`, and repairing afterwards.

## Repair: `devx session markers`

Restores the exact marker a reviewed backup holds for the exact record, when
every independent witness agrees. It runs as a dry run by default, needs an
explicit owner confirmation, is applied against a hash, and is journaled and
reversible.

```
devx session markers --backup <sessions.json backup> [...]                       # review (read-only)
devx session markers --backup ... --confirm <agent-id> [...]                     # plan including confirmed agents
devx session markers --backup ... --confirm ... --apply <plan-hash>              # apply exactly that plan
devx session markers --rollback <backup-dir>                                     # undo
```

A marker is proposed (as a candidate) only if **all** of these hold, otherwise
the agent is listed as `skip` with a reason:

| Witness | Requirement |
|---|---|
| agent | `created_by=mcp`, not adopted, not retired |
| claimants | the only live agent claiming the session, and the only live agent and session using the worktree |
| record | exists, has **no** marker of any kind, same path as the agent's worktree, same project, not a container, and no ports or routes (a local-only session never has them; a record with them may have been published, and `session rm` skips route cleanup for marked sessions) |
| ordering | session `created_at` ≤ agent `created_at`, and the gap is ≤ 2 minutes (start writes the record, then the agent) |
| instance | if the agent is bound, the record is that instance; if the record has an id, the agent recorded it, or it is the id the instances migration derives for this exact record |
| creation event | the agent's own first `agent_created` event names this session (and its instance id, if one is recorded) |
| live tmux | the tmux session of that exact name exists and has `DEVX_LOCAL_ONLY_AGENT=<agent-id>`, which the local-only creator sets atomically at creation |
| backups | at least one reviewed backup holds a record with the same `created_at` and path carrying `local_only{owner: devx-pi-mcp, agent_id: <agent>, created_at: <record created_at>}`. No backup of that record carries a different marker, an adoption marker or a different instance id. Backups taken after the loss (no marker) are simply not witnesses |

Backups must be regular files (no symlinks) owned by the current user, not
group- or world-writable, and not the live `sessions.json`. The checks run on
the opened file, and its bytes are read from the same handle. Backup paths
and SHA-256 values are part of the plan hash.

How strong is the evidence? Every witness (agent records, event logs, tmux
environment, backups) can be written by any process running as the same
user, including a managed agent. The witnesses rule out accidental and
ambiguous cases. **The owner's explicit `--confirm` after reading the dry run
is the real authorization gate.** Likewise, the `DEVX_PI_AGENT_ID` refusal is
a guardrail against an agent repairing its own ownership by accident, not a
security boundary.

`--apply`:

1. re-plans and refuses unless the hash equals the reviewed one (any change
   to records, backups, tags or confirmations changes it);
2. refuses inside a managed agent (`DEVX_PI_AGENT_ID` set), as `--rollback` does;
3. re-hashes every backup and refuses if one changed;
4. copies `sessions.json` and the plan into a fresh `backups/session-markers-*` directory;
5. for each confirmed agent, under the agent lock: re-checks the agent's
   identity (created_at, binding, session, worktree, not retired or adopted)
   and the live tmux tag. Then, under the sessions lock, re-checks the exact
   record (created_at, path, instance id, project, no marker), checks that no
   other record already carries this agent's marker, journals (fsync), and sets
   `local_only` (no other field of any record changes; the file itself is
   re-serialised by the current schema like every DevX write, so retired
   fields such as `color` are dropped);
6. appends a `local_only_marker_restored` event to the agent. This event is a
   notification only: the authoritative record of each write is the journal
   entry, fsynced before the write. A crash between the write and the event
   leaves the marker restored and journaled, but with no event.

A marker that is already present and identical counts as done, so a concurrent
apply, or re-running the same `ApplyMarkerPlan`, writes nothing twice. From the
CLI after an interruption, re-run the same dry run with the same `--confirm`
list: already-restored agents show `marker_present`, their confirmations still
count, and the new plan hash covers only what remains. Apply that new hash.

`--rollback <dir>`:
- accepts only a `session-markers-*` run directory in the backups directory
  next to `sessions.json`, whose `plan.json` and `journal.jsonl` are trusted
  files and whose journal entries each match a confirmed restoration in that
  plan (agent, session record identity and exact marker);
- refuses if a later (or same-timestamp) run that was not rolled back exists
  (two runs can write identical markers), unless `--force-older`;
- removes a marker only if the record still has the journaled `created_at`
  and path, and still holds exactly the marker written. The instance id is
  compared when both the journal and the record have one: the instances
  migration may assign an id after the repair.

The repair never changes agent records or bindings, instance ids, leases,
queues, tmux sessions, panes or processes.

## Future rollout requirements (not part of this change)

These are owner-controlled steps. None of them is performed by this PR.

1. Merge order: this branch stacks on #72 (which contains #71). It must land
   after them, or be rebased if they change.
2. Install a build containing this change everywhere DevX writes
   `sessions.json` (CLI in `~/projects/go/bin`, `/usr/local/bin`, the DevX app
   and desktop sibling CLI). Until **every** writer includes it, any older
   writer can strip markers again; the repair can then simply be re-run.
3. Stop using main-based builds (`go run`, tests with a real HOME, old app
   bundles) against the real `~/.config/devx` until main contains #71/#72 and
   this change.
4. Before applying a repair, the owner reviews the dry run, picks the
   backups and confirms each agent explicitly. Read the `skip` reasons. A
   skipped agent is **not** proven and must be resolved another way (for
   example by retiring it).
5. The marker repair and the session instance migration are separate
   commands. Running `devx session instances` before or after is fine: the
   repair accepts the instance id that migration derives.
6. Relaunching an agent after repair is a separate owner decision
   (`devx agent relaunch <id> [--force]`); `--force` replaces a live Pi.
