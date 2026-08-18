# Harness Wrapper Research and DevX Opportunities

Research covered Codex app/web, Claude Desktop and Claude Code, Conductor, Pi and community wrappers, OpenCode/Crush, Cursor Cloud Agents, GitHub Copilot coding agent, Jules, Vibe Kanban, and Aider. Observed product behavior is separated from recommendations below.

## Status and sequencing

The current priority is shell fidelity: preserve and clarify the production session, terminal, artifact, lifecycle, responsive, and notification capabilities before adding product scope. The enhancement ideas below remain a tracked post-shell backlog. They should advance only after their data, security, and interaction contracts are explicitly scoped.

## Observed convergence

- **Project → isolated workspace/worktree → session/task** is the dominant hierarchy in Codex, Conductor, Cursor, and related tools.
- **Status outside the transcript** is essential: running, waiting, needs input, failed, review ready, and completed states appear in rows, cards, inboxes, or PR checks.
- **Git context is compact but persistent:** branch/worktree identity, dirty state, diffs, and review/checkpoint actions are treated as trust cues.
- **Consequential actions pause explicitly:** Claude Code, Codex, and OpenCode expose approval modes or ask/allow/deny policy.
- **Evidence outlives chat:** diffs, plans, test output, logs, previews, generated files, and PRs become reviewable artifacts.
- **Mobile is a control plane:** dispatch, monitor, answer, approve, review bounded changes, and hand off to desktop. Full terminal parity is not the strongest demonstrated mobile use case.
- **Cross-device continuity matters:** Claude Code Remote Control, Codex mobile/web, Cursor agents, and GitHub Mobile preserve task context while execution stays elsewhere.

## Recommended UI and UX enhancements

### Refine existing DevX capabilities

1. **Canonical session status language.** Use one derived model for sidebar rows, top chrome, overlays, notifications, and mobile. Distinguish active, waiting, attention, dirty, stale, broken, and unknown without relying only on color.
2. **Truthful context header.** Keep project, session, branch/worktree, target, routes, artifacts, and tmux windows visible but progressively disclosed.
3. **Attention-first navigation.** Preserve project grouping while elevating flagged or actionable sessions into a small, non-duplicative attention summary.
4. **Evidence-oriented artifacts.** Make plans, diffs, test results, logs, screenshots, previews, and handoff notes easier to scan by type and provenance.
5. **Progressive terminal layers.** Keep the real terminal, but let users move between concise status/activity, terminal, changes, and artifacts without turning the shell into a permanent four-pane IDE.
6. **Notification routing.** Alert only for attention, approval, failure, completion, conflict, and explicit artifact focus—not normal streaming activity.
7. **Lifecycle visibility.** Surface bootstrap/cleanup step, elapsed time, output, failure, and retry as session status rather than requiring terminal archaeology.

### Potential product additions requiring explicit scope

1. **Actionable attention inbox.** Aggregate approvals, failures, conflicts, completed tasks, and unread/focused artifacts. This is a real feature, not merely new chrome, and needs a canonical event model first.
2. **Risk-tiered approval surface.** Show operation, command/tool, cwd/host, affected scope, and policy duration: once, session, project, deny. Remote approvals require threat modeling.
3. **Compact Git control strip.** Branch, ahead/behind, changed-file count, conflicts, last checkpoint, view diff, checkpoint, revert, and open PR—without becoming a full Git client.
4. **Named repeatable workflows.** Borrow Codex Skills/Automations and lifecycle-script visibility for reusable setup, verification, cleanup, and scheduled work.
5. **Shareable run/session view.** A bounded read-only link for status and selected artifacts can reduce handoff friction, but must preserve DevX's local/private trust boundary.
6. **Cross-device deep links.** Notifications should open the exact session, approval, artifact, or failure and preserve selected context when handed back to desktop.

## Mobile-specific opportunities

1. Optimize around five jobs: **see what is running, understand attention, approve/deny bounded actions, inspect a small artifact/diff, and dispatch/resume work**.
2. Use session and status **bottom sheets** with strong focus management; keep the terminal mounted when switching context where feasible.
3. Prioritize a read-only activity/terminal tail with interrupt/stop and safe predefined actions over a tiny full workstation UI.
4. Make host identity, connectivity, stale/offline state, and last update explicit before allowing remote actions.
5. Keep execution, repository access, ports, and secrets on the DevX host; expose an authenticated control/event channel rather than moving the runtime to the phone.
6. Use push/deep-link actions for approval, failure, completion, and focused artifacts; redact sensitive command and repository content on lock screens.
7. Hand large diffs, complex merges, and interactive terminal work back to desktop with context preserved.

## Anti-patterns to avoid

- Flat chat history that hides repository/worktree identity.
- Status represented only by transcript prose or animation.
- Generic “Allow?” prompts without command, cwd, scope, or duration.
- Badge counts for every streaming event.
- Permanent multi-pane IDE layouts on phones.
- Unsupported “healthy,” runtime, git, or route metrics presented as fact.
- Separate status implementations for sidebar, terminal, overlay, notification, and mobile that can drift.
- Mandatory kanban bookkeeping when session/branch lifecycle can derive state automatically.

## Primary sources

- [OpenAI — Codex app](https://openai.com/index/introducing-the-codex-app/)
- [OpenAI — Codex from anywhere](https://openai.com/index/work-with-codex-from-anywhere/)
- [OpenAI — Codex approvals and security](https://developers.openai.com/codex/agent-approvals-security)
- [Anthropic — Claude Code Remote Control](https://code.claude.com/docs/en/remote-control)
- [Anthropic — Claude Code permissions](https://docs.anthropic.com/en/docs/claude-code/permissions)
- [Anthropic — Claude Code hooks](https://docs.anthropic.com/en/docs/claude-code/hooks-guide)
- [Conductor workflow](https://www.conductor.build/docs/concepts/workflow)
- [Pi monorepo](https://github.com/badlogic/pi-mono)
- [OpenCode](https://github.com/opencode-ai/opencode) and successor [Crush](https://github.com/charmbracelet/crush)
- [Cursor Cloud Agents](https://cursor.com/docs/cloud-agent)
- [GitHub Copilot agents](https://github.com/features/copilot/agents)
- [Google Jules](https://jules.google/docs/)
- [Vibe Kanban](https://github.com/BloopAI/vibe-kanban)
- [Aider Git integration](https://aider.chat/docs/git.html)

