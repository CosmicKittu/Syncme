#!/usr/bin/env bash
# Builds the Android APK into dist/syncme.apk.
# Needs only a JDK 17+; the Android SDK is downloaded automatically into
# ~/.cache/syncme/android-sdk if ANDROID_HOME is not set.
#   Arch:   sudo pacman -S --needed jdk17-openjdk unzip
#   Debian: sudo apt install openjdk-17-jdk-headless unzip
# Usage: scripts/build-android.sh [debug|release]   (default: release)
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)
TYPE=${1:-release}
CMDLINE_TOOLS_URL="https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip"

if ! command -v java >/dev/null; then
  echo "Java not found. Install a JDK 17+ (Arch: sudo pacman -S jdk17-openjdk)" >&2; exit 1
fi
JV=$(java -version 2>&1 | awk -F'"' '/version/ {print $2}' | cut -d. -f1)
if [ "${JV:-0}" -lt 17 ]; then
  echo "Java $JV found, but 17 or newer is required." >&2; exit 1
fi

if [ -z "${ANDROID_HOME:-}" ] && [ -n "${ANDROID_SDK_ROOT:-}" ]; then ANDROID_HOME=$ANDROID_SDK_ROOT; fi
if [ -z "${ANDROID_HOME:-}" ]; then
  ANDROID_HOME="${XDG_CACHE_HOME:-$HOME/.cache}/syncme/android-sdk"
fi
export ANDROID_HOME
SDKMANAGER="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
if [ ! -x "$SDKMANAGER" ]; then
  echo "Downloading Android command-line tools into $ANDROID_HOME ..."
  command -v unzip >/dev/null || { echo "unzip is required" >&2; exit 1; }
  mkdir -p "$ANDROID_HOME/cmdline-tools"
  tmp=$(mktemp -d)
  curl -fL --retry 3 -o "$tmp/tools.zip" "$CMDLINE_TOOLS_URL"
  unzip -q "$tmp/tools.zip" -d "$tmp"
  rm -rf "$ANDROID_HOME/cmdline-tools/latest"
  mv "$tmp/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest"
  rm -rf "$tmp"
fi
if [ ! -d "$ANDROID_HOME/platforms/android-35" ] || [ ! -d "$ANDROID_HOME/build-tools/35.0.0" ]; then
  echo "Installing Android SDK packages (accepting licenses) ..."
  yes | "$SDKMANAGER" --licenses >/dev/null || true
  "$SDKMANAGER" "platforms;android-35" "build-tools;35.0.0" "platform-tools"
fi

cd android
TASK=assembleRelease; [ "$TYPE" = debug ] && TASK=assembleDebug
./gradlew --no-daemon "$TASK"
mkdir -p "$ROOT/dist"
cp "app/build/outputs/apk/$TYPE/app-$TYPE.apk" "$ROOT/dist/syncme.apk"
echo
echo "Built dist/syncme.apk"
echo "Install on a phone connected by USB (USB debugging on):"
echo "  $ANDROID_HOME/platform-tools/adb install -r $ROOT/dist/syncme.apk"
echo "or copy the file to the phone and open it."
