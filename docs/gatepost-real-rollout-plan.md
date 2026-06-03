# Gatepost-backed DevX real rollout implementation plan

## Goal

Roll out `devx --target gatepost` for the real local DevX/Pi setup with **strict provider readiness**: session creation must fail if expected subscription/provider credentials are not available through Gatepost secret injection.

Baseline required providers:

```yaml
gatepost:
  required_providers: codex-oauth,openai-key,cliproxy-key
```

`cliproxy-key` is required because the current `models.container.json` Anthropic CLI/Antigravity routes use the host CLIProxy service. `GEMINI_API_KEY` is not registered by the current copied bootstrap helper; add explicit Gemini bootstrap support before making `gemini-key` required.

## What strict readiness means

`required_providers` is a startup gate, not a provider filter.

The bootstrap still attempts to register all supported credentials it can find. The required list controls whether DevX is allowed to continue after bootstrap.

With:

```yaml
gatepost:
  required_providers: codex-oauth,openai-key,cliproxy-key
```

DevX will fail `devx session create --target gatepost` unless all three are registered with the Gatepost control plane.

Provider sources today:

| Required provider | Source |
| --- | --- |
| `codex-oauth` | host Pi auth: `~/.pi/agent/auth.json`, key `openai-codex` |
| `openai-key` | host env: `OPENAI_API_KEY` |
| `cliproxy-key` | host env: `CLIPROXYAPI_API_KEY`; required because `models.container.json` Anthropic CLI/Antigravity routes use `host.docker.internal:8317` |

`anthropic-oauth` is bootstrapped if present but not required — the Anthropic OAuth path is rarely used since CLIProxy covers it.

So fixing Anthropic/Codex means repairing host Pi OAuth. Fixing `openai-key` means ensuring the installed DevX/web/session-create environment has `OPENAI_API_KEY` available. Fixing `cliproxy-key` means ensuring `CLIPROXYAPI_API_KEY` is present and the host CLIProxy service on `host.docker.internal:8317` is reachable/healthy for the current Pi model routes.

## Current state

Implemented and tested in `/Users/jfox/projects/devx/.worktrees/jf-docker`:

- `target=gatepost` starts a Docker/mitmproxy Gatepost runtime.
- Agent container is internal-network only.
- Gatepost proxy owns egress.
- Provider credentials are registered into Gatepost from host-side helpers/env.
- Agent receives placeholders only.
- Gatepost Logs UI is linked through DevX web.
- Provider readiness is stored in session metadata and exposed by web/TUI.
- `gatepost.required_providers` fails session creation if required providers were not registered.

Known local blocker from previous smoke runs:

- Anthropic OAuth refresh returned `403 Forbidden`.
- Strict readiness with `anthropic-oauth` will intentionally fail until host Pi Anthropic OAuth is repaired.

## Fixing Anthropic OAuth readiness

### Why Anthropic currently fails

The Gatepost bootstrap helper reads host Pi auth from `~/.pi/agent/auth.json`. For Anthropic it uses:

- `auth["anthropic"].access`
- `auth["anthropic"].refresh`
- `auth["anthropic"].expires`

If the token is expired or near expiry, it calls Anthropic's OAuth token endpoint. A `403` means the stored refresh token/client combination is no longer accepted. The expected fix is to refresh/recreate the host Pi Anthropic login, not to silently skip it.

### Repair procedure

1. Backup host Pi auth before changing it:

   ```bash
   AUTH_BACKUP="$HOME/.pi/agent/auth.json.backup.$(date +%Y%m%d-%H%M%S)"
   cp ~/.pi/agent/auth.json "$AUTH_BACKUP"
   chmod 600 "$AUTH_BACKUP"
   echo "backup: $AUTH_BACKUP"
   ```

2. Re-authenticate Anthropic in the host Pi setup.

   Preferred path: run the supported Pi login flow from the normal host environment, not from a temporary HOME/container:

   ```bash
   pi /login
   ```

   Complete the Anthropic/Claude login path. If Pi exposes a provider-specific Anthropic login/logout command, use that instead.

   Do **not** hand-edit `~/.pi/agent/auth.json` interactively. Treat it as Pi-owned state. If supported login/logout cannot repair it, add a small purpose-built reset tool that atomically rewrites the JSON with `0600` permissions, or fix Pi's login flow.

3. Verify host Pi auth presence/expiry without printing secrets:

   ```bash
   python3 - <<'PY'
   import json, os, time
   p = os.path.expanduser('~/.pi/agent/auth.json')
   auth = json.load(open(p))
   for key in ['anthropic', 'openai-codex']:
       item = auth.get(key) or {}
       has_access = bool(item.get('access'))
       has_refresh = bool(item.get('refresh'))
       exp = item.get('expires')
       bits = [f'access={has_access}', f'refresh={has_refresh}']
       if exp:
           bits.append(f'expires_in_minutes={int((exp - time.time()*1000)/60000)}')
       print(key, ', '.join(bits))
   PY
   ```

4. Ensure direct OpenAI key is available to DevX startup:

   ```bash
   test -n "${OPENAI_API_KEY:-}" || { echo 'OPENAI_API_KEY is not set'; exit 1; }
   test -n "${CLIPROXYAPI_API_KEY:-}" || { echo 'CLIPROXYAPI_API_KEY is not set'; exit 1; }
   ```

5. Validate by running the strict DevX smoke in Phase 4. Expected session metadata should include:

   - `anthropic-oauth`
   - `codex-oauth`
   - `openai-key`
   - `cliproxy-key`

If Anthropic OAuth still returns `403`, the bootstrap helper will log a warning and skip it. That is acceptable because `anthropic-oauth` is not required; CLIProxy (`cliproxy-key`) covers Anthropic model access through `host.docker.internal:8317`.

## Phase 1 — build stable local artifacts

### 1. Build Gatepost images

```bash
cd /Users/jfox/projects/gatepost/.worktrees/jf-gatepost-simple
scripts/build-images.sh
```

### 2. Build/install Gatepost Logs binary

```bash
cd /Users/jfox/projects/gatepost/.worktrees/jf-logs-ui
install_dir="$HOME/.local/bin"
mkdir -p "$install_dir"
go build -o "$install_dir/gatepost-logs" ./cmd/gatepost-logs
chmod 755 "$install_dir/gatepost-logs"
```

### 3. Install a stable provider-bootstrap artifact

For this rollout, copy the reviewed helper out of the mutable worktree into a user-owned stable location. This is still a Python helper, but it avoids DevX executing directly from a development checkout.

```bash
src=/Users/jfox/projects/gatepost/.worktrees/jf-gatepost-simple/scripts/make-pi-tokens.py
dst_dir="$HOME/.local/lib/gatepost"
dst="$dst_dir/gatepost-provider-bootstrap.py"
mkdir -p "$dst_dir"
chmod 700 "$dst_dir"
install -m 700 "$src" "$dst"
ls -l "$dst"
```

Preflight ownership/perms:

```bash
python3 - <<'PY'
import os, stat
paths = [os.path.expanduser('~/.local/bin/gatepost-logs'), os.path.expanduser('~/.local/lib/gatepost/gatepost-provider-bootstrap.py')]
for p in paths:
    st = os.stat(p)
    assert st.st_uid == os.getuid(), f'{p} not owned by current user'
    assert not (st.st_mode & stat.S_IWOTH), f'{p} is world-writable'
    assert not (st.st_mode & stat.S_IWGRP), f'{p} is group-writable'
    print('ok', p)
PY
```

Follow-up hardening: replace this copied Python helper with a packaged/versioned `gatepost-provider-bootstrap` artifact.

### 4. Build DevX in a private staging directory

```bash
cd /Users/jfox/projects/devx/.worktrees/jf-docker
cd web/app && npm run build
cd ../..
stage_dir="$(mktemp -d)"
chmod 700 "$stage_dir"
go build -o "$stage_dir/devx" .
"$stage_dir/devx" --version
"$stage_dir/devx" session create --help | grep gatepost
mkdir -p "$HOME/.local/share/devx"
printf '%s\n' "$stage_dir" > "$HOME/.local/share/devx/gatepost-devx-stage-dir"
chmod 600 "$HOME/.local/share/devx/gatepost-devx-stage-dir"
```

Phase 3 reloads the staging path from `$HOME/.local/share/devx/gatepost-devx-stage-dir`, so the install step works even in a later shell.

## Phase 2 — configure real user-global DevX

Edit `~/.config/devx/config.yaml` (user-global, not project `.devx/config.yaml`). Keep existing config values such as `basedomain`, ports, Caddy, and `web_secret_token`.

If DevX web is used, `web_secret_token` must be set:

```yaml
web_secret_token: "<existing-random-secret>"
web_port: 7777 # or your existing value
```

Add/merge:

```yaml
gatepost:
  agent_image: gatepost-pi-agent:latest
  logs_command: /Users/jfox/.local/bin/gatepost-logs
  provider_bootstrap_command: /usr/bin/python3 /Users/jfox/.local/lib/gatepost/gatepost-provider-bootstrap.py
  auth_home: /Users/jfox
  required_providers: codex-oauth,openai-key,cliproxy-key
```

Notes:

- Prefer trusted absolute paths for executable Gatepost settings.
- DevX reads executable Gatepost settings only from user-global config, explicit `--config`, or explicit env; not from project `.devx/config.yaml`.
- `provider_bootstrap_command` is stable local artifact for this rollout, not a mutable repo script.
- `auth_home` points the helper at real host Pi auth even if a test uses temporary HOME.
- `required_providers` is strict by design and must be kept in sync with the provider/model set you expect to use.

## Phase 3 — install DevX binary with rollback

1. Locate current binary and define backup path:

   ```bash
   OLD_DEVX="$(which devx)"
   DEVX_BACKUP="${OLD_DEVX}.backup.$(date +%Y%m%d-%H%M%S)"
   echo "old: $OLD_DEVX"
   echo "backup: $DEVX_BACKUP"
   ```

2. Backup it:

   ```bash
   cp "$OLD_DEVX" "$DEVX_BACKUP"
   chmod 755 "$DEVX_BACKUP"
   ```

3. Stop web daemon if running:

   ```bash
   devx web stop || true
   ```

4. Install new binary from private staging dir:

   ```bash
   stage_dir="$(cat "$HOME/.local/share/devx/gatepost-devx-stage-dir")"
   install -m 755 "$stage_dir/devx" "$OLD_DEVX"
   ```

5. Restart web daemon:

   ```bash
   devx web --daemon
   ```

Rollback:

```bash
devx web stop || true
install -m 755 "$DEVX_BACKUP" "$OLD_DEVX"
devx web --daemon
```

## Phase 4 — strict smoke test on a low-risk project

### 1. Create a smoke session

```bash
devx session create gp-smoke --project <safe-project-alias> --target gatepost --no-tmux
```

If this fails because a required provider is missing, stop and fix the provider. That is the intended behavior.

### 2. Confirm provider readiness

```bash
python3 - <<'PY'
import json, os
p = os.path.expanduser('~/.config/devx/sessions.json')
s = json.load(open(p))['sessions']['gp-smoke']['target']['gatepost']
print('mode:', s.get('provider_mode'))
print('registered:', ','.join(s.get('registered_providers', [])))
print('warnings:', s.get('provider_warnings', []))
required = {'codex-oauth', 'openai-key', 'cliproxy-key'}
registered = set(s.get('registered_providers', []))
missing = required - registered
assert not missing, f'missing required providers: {missing}'
PY
```

### 3. Confirm secret isolation with targeted checks

Do not dump full environment to `/tmp`.

```bash
# Agent should have placeholders, not real secrets.
docker inspect devx-gp-smoke --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep '^OPENAI_API_KEY=GATEPOST_SECRET:openai-key$'

docker inspect devx-gp-smoke --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep '^CLIPROXYAPI_API_KEY=GATEPOST_SECRET:cliproxy-key$'

docker inspect devx-gp-smoke --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep '^GATEPOST_EVENTS_TOKEN=' >/dev/null

# Control token must not be in agent env.
! docker inspect devx-gp-smoke --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep '^GATEPOST_CONTROL_TOKEN='

# Host raw provider keys must not appear in agent env. Do this in Python so
# secrets are not passed as grep argv.
python3 - <<'PY'
import os, subprocess, sys
text = subprocess.check_output([
    'docker', 'inspect', 'devx-gp-smoke',
    '--format', '{{range .Config.Env}}{{println .}}{{end}}',
], text=True)
failed = False
for name in ['OPENAI_API_KEY', 'CLIPROXYAPI_API_KEY']:
    value = os.environ.get(name)
    if value and value in text:
        print(f'raw {name} found in agent env', file=sys.stderr)
        failed = True
if failed:
    sys.exit(1)
PY
```

Also check persisted metadata/logs do not contain host secrets or OAuth tokens. This script prints only failure labels, not token values:

```bash
python3 - <<'PY'
import json, os, pathlib, sys
paths = [pathlib.Path(os.path.expanduser('~/.config/devx/sessions.json'))]
gp = pathlib.Path(os.path.expanduser('~/.local/share/devx/gatepost/gp-smoke'))
if gp.exists():
    paths.extend([p for p in gp.rglob('*') if p.is_file()])
needles = []
if os.environ.get('OPENAI_API_KEY'):
    needles.append(('OPENAI_API_KEY', os.environ['OPENAI_API_KEY']))
if os.environ.get('CLIPROXYAPI_API_KEY'):
    needles.append(('CLIPROXYAPI_API_KEY', os.environ['CLIPROXYAPI_API_KEY']))
auth_path = pathlib.Path(os.path.expanduser('~/.pi/agent/auth.json'))
if auth_path.exists():
    auth = json.load(open(auth_path))
    for provider in ['anthropic', 'openai-codex']:
        item = auth.get(provider) or {}
        for field in ['access', 'refresh']:
            value = item.get(field)
            if value:
                needles.append((f'{provider}.{field}', value))
failed = False
for path in paths:
    try:
        data = path.read_text(errors='ignore')
    except Exception:
        continue
    for label, value in needles:
        if value and value in data:
            print(f'secret leak detected: {label} in {path}', file=sys.stderr)
            failed = True
if failed:
    sys.exit(1)
print('secret persistence check passed')
PY
```

### 4. Confirm egress isolation

```bash
docker exec devx-gp-smoke env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy \
  curl -fsS --max-time 5 https://example.com
```

Expected: command fails.

### 5. Confirm real OpenAI provider access through Gatepost injection

```bash
docker exec devx-gp-smoke sh -lc \
  'curl -fsS --max-time 30 -H "Authorization: Bearer $OPENAI_API_KEY" https://api.openai.com/v1/models | grep -q object'
```

### 6. Confirm Codex/CLIProxy/OpenAI functional paths

Registration proves Gatepost has secrets, but broad rollout also requires exact in-session provider proof. Run these commands from inside `devx-gp-smoke`.

`anthropic-oauth` is not required and therefore not tested here; CLIProxy covers Anthropic model access.

CLIProxy-backed Anthropic route (`cliproxy-key`) using the current `models.container.json` `anthropic-cli` provider:

```bash
bash -o pipefail -lc 'docker exec devx-gp-smoke pi --provider anthropic-cli --model claude-sonnet-4-6 \
  --no-tools --no-extensions --no-skills --no-context-files --no-session \
  --print "Respond with exactly: cliproxy-ok" \
  | grep -F "cliproxy-ok"'
```

Codex OAuth path (`codex-oauth`) using the current `models.container.json` `openai-codex` provider:

```bash
bash -o pipefail -lc 'docker exec devx-gp-smoke pi --provider openai-codex --model gpt-5.3-codex \
  --no-tools --no-extensions --no-skills --no-context-files --no-session \
  --print "Respond with exactly: codex-ok" \
  | grep -F "codex-ok"'
```

Note: Codex has a 5-minute rate-limit window. A `usage_limit_reached` 429 error means the secret injection is working correctly — the request reached Codex and was authenticated. Wait for the window to reset and retry. This is not a Gatepost failure.

Acceptance criterion: all three commands exit 0 and return the expected marker text. If any fail because a model alias has changed, first confirm the replacement with `docker exec devx-gp-smoke pi --list-models <provider-or-model-search>`, update this plan with the exact working command, and rerun before broad rollout.

### 7. Confirm smart eval path

```bash
TOKEN=$(docker inspect devx-gp-smoke --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^GATEPOST_EVENTS_TOKEN=/{print $2}')

docker exec -e TOKEN="$TOKEN" devx-gp-smoke sh -lc \
  'curl -fsS -XPOST http://gatepost-events:9100/v1/events \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "[{\"type\":\"task_set\",\"source\":\"smoke\",\"payload\":{\"task\":\"Fetch example.com as a Gatepost smoke test\"}},{\"type\":\"role_set\",\"source\":\"smoke\",\"payload\":{\"role\":\"DevX Gatepost smoke tester\"}}]" >/dev/null'

docker exec devx-gp-smoke curl -fsS --max-time 60 https://example.com >/dev/null
grep '"evaluator": "smart"' ~/.local/share/devx/gatepost/gp-smoke/audit/audit.jsonl
```

### 8. Confirm logs link and UI/TUI details

- Open DevX web UI.
- Locate `gp-smoke`.
- Confirm Gatepost provider metadata appears in the session payload/UI.
- Click Gatepost Logs.
- Confirm audit entries render.
- Open TUI and confirm Gatepost details include status/logs/providers.

Capture at least one screenshot or terminal artifact for the rollout record.

### 9. Confirm host-only bypass/enforce controls

```bash
devx session gatepost bypass gp-smoke
# Bypass attaches the agent to the egress network; direct egress without proxy must work.
docker exec devx-gp-smoke env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy \
  curl -fsS --max-time 10 https://example.com >/dev/null

devx session gatepost enforce gp-smoke
# Enforce removes direct egress again; direct egress without proxy must fail.
! docker exec devx-gp-smoke env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy \
  curl -fsS --max-time 5 https://example.com >/dev/null
```

Acceptance criterion: direct egress succeeds only while bypass is active and fails again after enforce.

The agent still must not have Docker socket, Gatepost control token, or any host control capability.

### 10. Remove smoke session

```bash
devx session rm gp-smoke --force
```

## Phase 5 — strict negative smoke

Prove strict readiness fails noisy.

Safest option: use a temporary explicit DevX config that requires a fake provider:

```bash
tmp_cfg="$(mktemp)"
cat > "$tmp_cfg" <<'YAML'
basedomain: localhost
ports: []
web_secret_token: test-token
gatepost:
  agent_image: gatepost-pi-agent:latest
  logs_command: /Users/jfox/.local/bin/gatepost-logs
  provider_bootstrap_command: /usr/bin/python3 /Users/jfox/.local/lib/gatepost/gatepost-provider-bootstrap.py
  auth_home: /Users/jfox
  required_providers: definitely-missing-provider
YAML

devx --config "$tmp_cfg" session create gp-negative --project <safe-project-alias> --target gatepost --no-tmux
```

Expected: session creation fails with a missing required provider error. Clean up any partial containers/session metadata if needed.

## Phase 6 — use for real sessions

After strict smoke and negative smoke pass:

```bash
devx session create <real-session> --project <project-alias> --target gatepost
```

If strict provider readiness fails, fix the host auth/provider issue first. Do not remove providers from `required_providers` unless you intentionally want sessions to start with that provider unavailable.

## Follow-up hardening

1. Package `gatepost-provider-bootstrap` as a stable versioned artifact instead of copied Python.
2. Add `devx gatepost doctor` to check:
   - Docker daemon
   - Gatepost images
   - `gatepost-logs` executable ownership/perms
   - provider bootstrap executable ownership/perms
   - Pi auth provider presence/expiry without printing secrets
   - strict provider readiness against a temporary control plane
3. Add `devx session gatepost smoke <session>` so normal validation uses stable host commands rather than `docker inspect/exec` internals.
4. Derive required providers from the active Pi model/provider configuration or a named Gatepost provider profile, reducing drift between model config and readiness policy.
5. Add explicit version/commit compatibility output for:
   - DevX binary
   - Gatepost proxy image
   - Gatepost Logs binary
   - provider bootstrap helper

## Open questions

- Should `gemini-key` be required if Gemini models are part of the default model set after Gemini bootstrap support is added?
- Should strict required providers eventually be derived from `models.container.json` instead of maintained manually in DevX config?
