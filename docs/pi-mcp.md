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
  `pi_mcp.allowed_projects`, and only creates a **new** host-target session.
  If the session already exists, the call is denied. MCP cannot attach to or
  control sessions that DevX didn't create through this interface.
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
  exercised by a real-Pi test. The human-submit race is reasoned from Pi's
  ordering, not deterministically exercised.
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
- **Package guard.** In `cmd`, `session` and `web`, a `TestMain` calls
  `tmuxfixture.RunGuarded`. It removes `TMUX`/`TMUX_PANE` and points
  `TMUX_TMPDIR` at a fresh private directory, so production code paths
  under test (exact `kill-session`, `list-sessions`, `has-session`) can't
  reach a real server.
- **Legacy tests.** Older tests that drive tmux or the real `sessions.json`
  without a fixture skip unless `DEVX_ALLOW_UNSCOPED_TMUX_TESTS=1` is set.
  CI sets it. On a machine with live DevX/tmux sessions, never set it.
  Affected tests: `session.TestTmuxSessionLaunch`,
  `web.TestPasteTmuxBufferKeepsMultilineTextAsOnePaste`, and the cmd session
  lifecycle tests.
