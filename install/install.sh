#!/usr/bin/env bash
set -euo pipefail

REPO="jfox85/devx"
INSTALL_DIR="${DEVX_INSTALL_DIR:-/usr/local/bin}"
VERSION="${DEVX_VERSION:-latest}"

usage() {
  cat <<USAGE
Install devx from GitHub releases.

Environment variables:
  DEVX_VERSION       Specific version (defaults to latest release)
  DEVX_INSTALL_DIR   Target directory for the devx binary (default: /usr/local/bin)
USAGE
}

command -v curl >/dev/null 2>&1 || { echo "curl is required to install devx" >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo "tar is required to install devx" >&2; exit 1; }

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"

case "$os" in
  linux|darwin)
    ;;
  *)
    echo "Unsupported operating system: $os" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64)
    arch="amd64"
    ;;
  arm64|aarch64)
    arch="arm64"
    ;;
  *)
    echo "Unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

if [[ "$VERSION" == "latest" ]]; then
  # The public redirect avoids GitHub's shared anonymous REST API rate limit.
  release_prefix="https://github.com/${REPO}/releases/tag/"
  if ! release_url="$(curl -fsSL "https://github.com/${REPO}/releases/latest" \
    -o /dev/null -w '%{url_effective}')"; then
    echo "Unable to determine latest release version from GitHub" >&2
    exit 1
  fi
  VERSION="${release_url#"$release_prefix"}"
  if [[ "$release_url" != "$release_prefix"* || ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
    echo "Unable to determine latest release version: unexpected GitHub release URL" >&2
    exit 1
  fi
else
  if [[ "$VERSION" != v* ]]; then
    VERSION="v${VERSION}"
  fi
fi

asset_version="${VERSION#v}"
asset="devx_${asset_version}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

echo "Downloading ${asset}..."
curl -fsSL "${url}" -o "${workdir}/${asset}"

echo "Extracting devx..."
tar -xzf "${workdir}/${asset}" -C "${workdir}"

mkdir -p "${INSTALL_DIR}"
install -m 0755 "${workdir}/devx" "${INSTALL_DIR}/devx"

echo "devx installed to ${INSTALL_DIR}/devx"
"${INSTALL_DIR}/devx" --version || true

# Point users at instructions matching the binary, rather than development docs.
echo "Documentation for ${VERSION}: https://github.com/${REPO}/blob/${VERSION}/README.md"
