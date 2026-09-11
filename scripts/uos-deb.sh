#!/usr/bin/env bash
# uos-deb.sh — assemble a 统信 UOS app-store-spec .deb (also installable on
# Kylin V10 desktop) from a built fairpeer-desktop binary.
#
# UOS store rules implemented here (docs/国产化适配实施方案.md §五/§10.7):
#   - /opt/apps/<appid>/ layout: info manifest + files/ + entries/
#   - info.version is strictly 4-segment numeric (X.Y.Z.0 from the git tag)
#   - Exec points inside files/ (launcher wrapper), icons under entries/icons
#   - no postinst system mutation, no $HOME writes (user state lives in XDG)
#   - depends name the WebKitGTK 4.0 runtime (the buster-leg binary links 4.0)
#
# The .deb is a human-download/store-submission artifact: cmd/sign's manifest
# skips .deb files, so the Linux updater channel stays the tarball.
#
# Usage: uos-deb.sh <binary> <arch: amd64|arm64> <version: X.Y.Z[.N]> <out.deb>
# Requires: dpkg-deb, imagemagick (convert) for the hicolor icon set.
set -euo pipefail

BIN=$1; ARCH=$2; VERSION=$3; OUT=$4
APPID=com.fairpeer.desktop
ROOT=$(cd "$(dirname "$0")/.." && pwd)
UOS_DIR="$ROOT/desktop/build/linux/uos"
ICON_SRC="$ROOT/desktop/build/appicon.png"

command -v dpkg-deb >/dev/null || { echo "uos-deb: dpkg-deb not found" >&2; exit 1; }
command -v convert >/dev/null || { echo "uos-deb: imagemagick (convert) not found — needed for the hicolor icon set" >&2; exit 1; }
[ -x "$BIN" ] || { echo "uos-deb: binary $BIN missing/not executable" >&2; exit 1; }

# 4-segment numeric version: dpkg and the UOS store both reject anything else.
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
  echo "uos-deb: version '$VERSION' is not numeric X.Y.Z[.N]" >&2; exit 1
fi
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || VERSION="$VERSION.0"

# dpkg Architecture names per CPU (verified against published pools 2026-09):
#   amd64 (Intel/AMD/兆芯/海光) · arm64 (飞腾/鲲鹏/海思麒麟) — pass through.
#   LoongArch is SPLIT BY WORLD: old-world dpkg (Loongnix 20 / UOS V20 /
#   Kylin V10) = loongarch64; new-world dpkg (Loongnix 25 / UOS V25 / Kylin
#   V11 / Debian) = loong64. sw64 = sw_64 (underscore). mips64el = dead
#   (UOS frozen at 1050). Our current legs are amd64/arm64 only; if a loong64
#   leg is ever added, map the world explicitly instead of trusting $ARCH.

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

APP="$STAGING/opt/apps/$APPID"
mkdir -p "$APP/files/bin" "$APP/entries/applications"

install -m 0755 "$BIN" "$APP/files/bin/fairpeer-desktop"
install -m 0755 "$UOS_DIR/launcher.sh" "$APP/files/bin/fairpeer"

for size in 16 24 32 48 128 256 512; do
  d="$APP/entries/icons/hicolor/${size}x${size}/apps"
  mkdir -p "$d"
  convert "$ICON_SRC" -resize "${size}x${size}" "$d/$APPID.png"
done

sed -e "s/@VERSION@/$VERSION/" -e "s/@ARCH@/$ARCH/" "$UOS_DIR/info.in" > "$APP/info"
sed "s/@APPID@/$APPID/g" "$UOS_DIR/app.desktop.in" > "$APP/entries/applications/$APPID.desktop"

mkdir -p "$STAGING/DEBIAN"
cat > "$STAGING/DEBIAN/control" <<EOF
Package: $APPID
Version: $VERSION
Architecture: $ARCH
Section: utils
Priority: optional
Depends: libgtk-3-0, libwebkit2gtk-4.0-37
Maintainer: zzycxz <zzycxz@users.noreply.github.com>
Description: fairpeer desktop - a Wails shell around the Go kernel
 Domestic-OS build: WebKitGTK 4.0, glibc 2.28 baseline (UOS 20 / Kylin V10).
EOF

dpkg-deb --build -Zxz "$STAGING" "$OUT"
echo "uos-deb: built $OUT ($(du -h "$OUT" | cut -f1))"
