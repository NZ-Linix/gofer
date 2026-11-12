#!/bin/sh
# gofer installer for macOS and Linux:
#   curl -fsSL https://raw.githubusercontent.com/NZ-Linix/gofer/main/install.sh | sh
set -eu

REPO="NZ-Linix/gofer"
BASE="https://github.com/$REPO/releases/latest/download"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) echo "Unsupported OS: $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="gofer_${os}_${arch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${asset}..."
curl -fsSL "$BASE/$asset" -o "$tmp/gofer"
curl -fsSL "$BASE/checksums.txt" -o "$tmp/checksums.txt"

expected="$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/gofer" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/gofer" | cut -d' ' -f1)"
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "Checksum verification failed." >&2
  exit 1
fi
chmod +x "$tmp/gofer"

dir="/usr/local/bin"
if [ -d "$dir" ] && [ -w "$dir" ]; then
  mv "$tmp/gofer" "$dir/gofer"
elif command -v sudo >/dev/null 2>&1; then
  echo "Installing to ${dir} (needs your password)..."
  sudo mkdir -p "$dir"
  sudo mv "$tmp/gofer" "$dir/gofer"
else
  dir="$HOME/.local/bin"
  mkdir -p "$dir"
  mv "$tmp/gofer" "$dir/gofer"
  case ":$PATH:" in *":$dir:"*) ;; *) echo "Add $dir to your PATH." ;; esac
fi

echo "✓ Installed gofer to $dir/gofer"
echo "  Next: gofer login <username>"
