#!/usr/bin/env bash
# build-hosts.sh — cross-compile the pure-Go fairpeer CLI for Linux and drop it
# into the desktop's host cache. The desktop's remote-workspace transports
# (WSL today; Docker next) install this binary on the remote side on first
# connect (~/.fairpeer/bin/fairpeer) and run `fairpeer host` over its stdio.
#
# Usage:
#   scripts/build-hosts.sh            # amd64 + arm64
#   scripts/build-hosts.sh amd64      # one arch
set -euo pipefail

cd "$(dirname "$0")/.."

archs=("${@:-amd64 arm64}")
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) cache="${LOCALAPPDATA}\\fairpeer\\hosts" ;;
  Darwin) cache="$HOME/Library/Caches/fairpeer/hosts" ;;
  *) cache="${XDG_CACHE_HOME:-$HOME/.cache}/fairpeer/hosts" ;;
esac
mkdir -p "$cache"

for arch in "${archs[@]}"; do
  out="$cache/fairpeer-linux-$arch"
  echo "building GOOS=linux GOARCH=$arch -> $out"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$out" ./cmd/fairpeer
done
echo "done. The desktop picks these up automatically on the next remote connect."
