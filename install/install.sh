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
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | \
    grep -m1 '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
  if [[ -z "$VERSION" ]]; then
    echo "Unable to determine latest release version" >&2
    exit 1
  fi
else
  if [[ "$VERSION" != v* ]]; then
    VERSION="v${VERSION}"
  fi
fi

asset="devx_${VERSION}_${os}_${arch}.tar.gz"
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
