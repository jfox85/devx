#!/usr/bin/env bash
# Print the devx version string to stamp into a build.
#
#   scripts/version.sh [--dirty]    version of the working tree (HEAD)
#   scripts/version.sh <commit>     version of a specific commit
#
# Only release tags published on origin (v[0-9]*) name a version, so local
# scratch tags, even v-prefixed ones like v1-scratch, never leak into
# `devx version`. If origin can't be reached it falls back to local tags of
# the strict form vMAJOR.MINOR.PATCH. With no matching tag it prints the short
# commit (plus -dirty); outside a git checkout it prints "dev".
# Written for bash 3.2 (macOS /bin/bash): no empty-array expansion under set -u.
set -euo pipefail

dirty=""
if [[ "${1:-}" == "--dirty" ]]; then
	dirty="--dirty"
	shift
fi
commit="${1:-HEAD}"
# --dirty describes the working tree, so it only makes sense for HEAD.
[[ "$commit" == HEAD ]] || dirty=""

cd "$(dirname "${BASH_SOURCE[0]}")/.."
if ! git rev-parse --verify --quiet "$commit^{commit}" >/dev/null 2>&1; then
	echo dev
	exit 0
fi

if remote="$(GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh} -o ConnectTimeout=5 -o BatchMode=yes" \
	git ls-remote --tags --refs origin 'v[0-9]*' 2>/dev/null)"; then
	set -- # positional params hold the --match args
	while IFS= read -r line; do
		[[ -n "$line" ]] && set -- "$@" --match "${line##*refs/tags/}"
	done <<<"$remote"
else
	set -- --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude 'v*[!0-9.]*'
fi

if [[ $# -gt 0 ]]; then
	if [[ -n "$dirty" ]]; then
		desc="$(git describe --tags --abbrev=7 "$@" --dirty 2>/dev/null)" && { echo "$desc"; exit 0; }
	else
		desc="$(git describe --tags --abbrev=7 "$@" "$commit" 2>/dev/null)" && { echo "$desc"; exit 0; }
	fi
fi

short="$(git rev-parse --short=7 "$commit")"
if [[ -n "$dirty" ]] && ! git diff --quiet HEAD -- 2>/dev/null; then
	short="$short-dirty"
fi
echo "$short"
