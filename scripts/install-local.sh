#!/usr/bin/env bash
# Reproducible local install of devx (CLI + optionally the macOS desktop app)
# from a clean, pushed commit.
#
#   scripts/install-local.sh            # CLI only, from origin/main
#   scripts/install-local.sh --app      # also rebuild and install DevX.app
#   scripts/install-local.sh --ref v1.2 # build a specific ref instead
#
# Guarantees:
# - Builds in a throwaway worktree at the exact commit, so local edits and
#   untracked files can never leak into an installed binary.
# - Refuses a ref that is not on the remote (no unpushed builds).
# - Stamps version/commit/date and verifies the installed binary reports
#   that commit.
# - Backs up the binary it replaces (one timestamped copy).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="origin/main"
WITH_APP=0
while [[ $# -gt 0 ]]; do
	case "$1" in
	--app) WITH_APP=1 ;;
	--ref)
		REF="$2"
		shift
		;;
	-h | --help)
		sed -n '2,16p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

for tool in git go npm; do
	command -v "$tool" >/dev/null || {
		echo "missing required tool: $tool" >&2
		exit 1
	}
done

cd "$ROOT"
git fetch -q origin
COMMIT="$(git rev-parse --verify "$REF^{commit}")"
if ! git branch -r --contains "$COMMIT" | grep -q .; then
	echo "refusing to build $REF ($COMMIT): it is not on any remote branch" >&2
	exit 1
fi
SHORT="$(git rev-parse --short "$COMMIT")"

GOBIN_DIR="$(go env GOBIN)"
[[ -n "$GOBIN_DIR" ]] || GOBIN_DIR="$(go env GOPATH)/bin"
DEST="$GOBIN_DIR/devx"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/devx-install.XXXXXX")"
cleanup() {
	git -C "$ROOT" worktree remove --force "$WORK/src" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT
git worktree add -q --detach "$WORK/src" "$COMMIT"
cd "$WORK/src"

# Only release tags (v*) name a version; local scratch tags must not leak into
# the version string.
VERSION="$(git describe --tags --match 'v[0-9]*' --always "$COMMIT" 2>/dev/null || echo "$SHORT")"
BUILD_DATE="$(date -u '+%Y-%m-%d_%H:%M:%S')"
LDFLAGS="-X github.com/jfox85/devx/version.Version=$VERSION -X github.com/jfox85/devx/version.GitCommit=$SHORT -X github.com/jfox85/devx/version.BuildDate=$BUILD_DATE"

echo "==> Building devx $VERSION ($SHORT) from $REF"
# The SPA is embedded from web/dist, which is committed; rebuild it and fail
# if the committed bundle is stale, so the binary matches the source.
(cd web/app && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
if ! git diff --quiet -- web/dist || [[ -n "$(git ls-files --others -- web/dist)" ]]; then
	echo "web/dist at $SHORT does not match web/app; refusing to install a mismatched bundle" >&2
	exit 1
fi
go build -trimpath -ldflags "$LDFLAGS" -o "$WORK/devx" .

reported="$("$WORK/devx" version)"
[[ "$reported" == *"($SHORT)"* ]] || {
	echo "built binary reports '$reported', expected commit $SHORT" >&2
	exit 1
}

mkdir -p "$GOBIN_DIR"
if [[ -f "$DEST" ]]; then
	cp -p "$DEST" "$DEST.previous"
	echo "    previous binary kept at $DEST.previous ($("$DEST" version 2>/dev/null || echo unknown))"
fi
install -m 0755 "$WORK/devx" "$DEST.new"
mv -f "$DEST.new" "$DEST"
echo "==> Installed $DEST: $("$DEST" version)"

if [[ "$WITH_APP" == 1 ]]; then
	echo "==> Building DevX.app"
	./desktop/build-macos-app.sh >/dev/null
	APP_SRC="$WORK/src/desktop/build/DevX.app"
	app_reported="$("$APP_SRC/Contents/MacOS/devx" version)"
	[[ "$app_reported" == *"($SHORT)"* ]] || {
		echo "bundled CLI reports '$app_reported', expected commit $SHORT" >&2
		exit 1
	}
	if pgrep -x devx-desktop >/dev/null; then
		echo "    DevX.app is running; quit it before installing" >&2
		exit 1
	fi
	rm -rf /Applications/DevX.app.previous
	[[ -d /Applications/DevX.app ]] && mv /Applications/DevX.app /Applications/DevX.app.previous
	cp -R "$APP_SRC" /Applications/DevX.app
	echo "==> Installed /Applications/DevX.app (bundled CLI: $app_reported)"
fi

on_path="$(command -v devx || true)"
if [[ "$on_path" != "$DEST" ]]; then
	echo "warning: 'devx' on PATH is $on_path, not $DEST" >&2
fi
echo "Restart any running 'devx web' to pick up the new binary."
