#!/usr/bin/env bash
# Syncme installer for macOS and Linux:
#   curl -fsSL https://raw.githubusercontent.com/CosmicKittu/Syncme/main/scripts/install.sh | bash
# Downloads the latest release into ~/.local/bin, then runs `syncme install`
# (starts it now and at every login). No root needed.
# Ubuntu/Debian and Arch users can install the .deb / pacman package instead.
set -euo pipefail

REPO="CosmicKittu/Syncme"
BIN_DIR="${SYNCME_BIN_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) os=macos ;;
  Linux)  os=linux ;;
  *) echo "Unsupported system: $(uname -s). On Windows use install-windows.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$BIN_DIR"
tmp=$(mktemp)
if [ -n "${SYNCME_BINARY:-}" ]; then        # local file (testing / offline)
  cp "$SYNCME_BINARY" "$tmp"
else
  url="https://github.com/$REPO/releases/latest/download/syncme-$os-$arch"
  echo "Downloading $url"
  curl -fL --retry 3 -o "$tmp" "$url"
fi
chmod 755 "$tmp"
# Stop a running copy so the file can be replaced.
if [ "$os" = macos ]; then
  launchctl unload "$HOME/Library/LaunchAgents/com.cosmickittu.syncme.plist" 2>/dev/null || true
else
  systemctl --user stop syncme 2>/dev/null || true
fi
mv "$tmp" "$BIN_DIR/syncme"
echo "Installed $BIN_DIR/syncme ($("$BIN_DIR/syncme" version))"

"$BIN_DIR/syncme" install

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    profile="$HOME/.profile"
    [ "$os" = macos ] && profile="$HOME/.zprofile"
    case "${SHELL:-}" in */bash) [ "$os" = macos ] && profile="$HOME/.bash_profile" ;; esac
    if ! grep -qs "$BIN_DIR" "$profile"; then
      printf '\n# added by the Syncme installer\nexport PATH="%s:$PATH"\n' "$BIN_DIR" >> "$profile"
    fi
    echo
    echo "Added $BIN_DIR to your PATH in $profile. Open a new terminal (or run: export PATH=\"$BIN_DIR:\$PATH\")."
    ;;
esac
echo
echo "Done. Now run:  syncme pair"
