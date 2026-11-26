#!/usr/bin/env bash
# Builds an Ubuntu/Debian package: packaging/deb/build-deb.sh <amd64|arm64> <version> <out-dir>
# Uses only dpkg-deb (no debhelper needed). The binary is static, so the
# package works on any Debian/Ubuntu release.
set -euo pipefail
ARCH=${1:?arch}; VERSION=${2:?version}; OUT=${3:-dist}
VERSION=${VERSION#v}
cd "$(dirname "$0")/../.."
ROOT=$(pwd)
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
P=$STAGE/pkg

CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$P/usr/bin/syncme" ./cmd/syncme
CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "-s -w" -o "$P/usr/bin/syncme-relay" ./cmd/syncme-relay

L=packaging/linux
install -Dm644 $L/syncme.service          "$P/usr/lib/systemd/user/syncme.service"
install -Dm644 $L/syncme-relay.service    "$P/usr/lib/systemd/system/syncme-relay.service"
install -Dm644 $L/syncme-send.desktop     "$P/usr/share/applications/syncme-send.desktop"
install -Dm644 $L/syncme.nemo_action      "$P/usr/share/nemo/actions/syncme.nemo_action"
install -Dm755 $L/syncme-dolphin.desktop  "$P/usr/share/kio/servicemenus/syncme.desktop"
install -Dm644 $L/syncme.ufw              "$P/etc/ufw/applications.d/syncme"
install -Dm644 README.md                  "$P/usr/share/doc/syncme/README.md"
install -Dm644 docs/PROTOCOL.md           "$P/usr/share/doc/syncme/PROTOCOL.md"

mkdir -p "$P/DEBIAN"
SIZE=$(du -sk "$P/usr" "$P/etc" | awk '{s+=$1} END {print s}')
cat > "$P/DEBIAN/control" <<CONTROL
Package: syncme
Version: $VERSION
Architecture: $ARCH
Maintainer: CosmicKittu <noreply@github.com>
Installed-Size: $SIZE
Recommends: wl-clipboard, xclip
Suggests: libnotify-bin
Section: net
Priority: optional
Homepage: https://github.com/CosmicKittu/Syncme
Description: peer-to-peer folder and clipboard sync with your Android phone
 Keeps ~/syncme on this computer in sync with Download/syncme on paired
 Android phones and tablets, directly over your Wi-Fi with end-to-end
 encryption, and shares the clipboard (text and images) between them.
CONTROL
echo "/etc/ufw/applications.d/syncme" > "$P/DEBIAN/conffiles"
cat > "$P/DEBIAN/postinst" <<'POSTINST'
#!/bin/sh
set -e
if [ "$1" = "configure" ]; then
  cat <<'MSG'

  Syncme installed. As your normal user (not root) run:

      systemctl --user enable --now syncme   # start syncing (also at every login)
      syncme install                         # right-click "Send to Phone" in Files (Nautilus)
      syncme pair                            # pair your phone: scan the QR code

  Firewall (only if ufw is enabled):  sudo ufw allow syncme

MSG
fi
exit 0
POSTINST
chmod 755 "$P/DEBIAN/postinst"

mkdir -p "$OUT"
DEB="$OUT/syncme_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group -Zxz --build "$P" "$DEB" >/dev/null
echo "$DEB"
