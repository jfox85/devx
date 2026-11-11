#!/usr/bin/env bash
# Validate the built binary's documented entry points before publishing it.
set -euo pipefail
binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT
run() {
  env HOME="$sandbox" USERPROFILE="$sandbox" XDG_CONFIG_HOME="$sandbox/.config" \
    DEVX_AUTO_CHECK_UPDATES=false "$binary" "$@"
}
cd "$sandbox"
for command in artifact agent ask; do
  run "$command" --help >/dev/null
done
run config init
test -f "$sandbox/.config/devx/config.yaml"
test -f "$sandbox/.config/devx/session.yaml.tmpl"
cp "$sandbox/.config/devx/config.yaml" "$sandbox/original.yaml"
run config init
cmp "$sandbox/original.yaml" "$sandbox/.config/devx/config.yaml"
# This is the README's project-config copy path.
mkdir .devx
cp "$sandbox/.config/devx/config.yaml" .devx/config.yaml
run --config "$sandbox/custom/config.yaml" config init
test -f "$sandbox/custom/config.yaml"
echo "Onboarding smoke passed: documented commands, config creation, preservation, and copy."
