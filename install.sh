#!/usr/bin/env sh
# Install the adviverse CLI from GitHub Releases of Adviverse/cli.
# Usage: curl -fsSL https://raw.githubusercontent.com/Adviverse/cli/main/install.sh | sh
# Env: ADVIVERSE_CLI_REPO (owner/repo), ADVIVERSE_CLI_VERSION (default: latest), ADVIVERSE_BIN_DIR (default: /usr/local/bin)
set -eu
REPO="${ADVIVERSE_CLI_REPO:-Adviverse/cli}"
BIN_DIR="${ADVIVERSE_BIN_DIR:-/usr/local/bin}"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os (download a release manually)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
ver="${ADVIVERSE_CLI_VERSION:-latest}"
if [ "$ver" = "latest" ]; then
  url="https://github.com/${REPO}/releases/latest/download/adviverse_${os}_${arch}.tar.gz"
else
  url="https://github.com/${REPO}/releases/download/${ver}/adviverse_${os}_${arch}.tar.gz"
fi
echo "downloading $url"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" | tar -xz -C "$tmp"
mkdir -p "$BIN_DIR" 2>/dev/null || true
if [ -w "$BIN_DIR" ]; then
  install -m 0755 "$tmp/adviverse" "$BIN_DIR/adviverse"
else
  sudo install -m 0755 "$tmp/adviverse" "$BIN_DIR/adviverse"
fi
echo "installed: $BIN_DIR/adviverse"
