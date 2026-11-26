#!/usr/bin/env bash
# End-to-end test: Go daemon + relay vs. the Android app's Kotlin core (on the JVM).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)
WORK=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT
PORT=47900
RELAY_PORT=47912

# Windows (Git Bash): native programs need .exe names and C:/... paths.
EXE=""; native() { echo "$1"; }
if [ "${OS:-}" = "Windows_NT" ]; then
  EXE=".exe"; native() { cygpath -m "$1"; }
fi
go build -o "$WORK/syncme$EXE" ./cmd/syncme
go build -o "$WORK/syncme-relay$EXE" ./cmd/syncme-relay
SYNCME="$WORK/syncme$EXE"

export SYNCME_HOME="$(native "$WORK/home")"
mkdir -p "$WORK/home/config" "$WORK/pc"
cat > "$WORK/home/config/config.json" <<EOF
{"name":"Test PC","folder":"$(native "$WORK/pc")","port":$PORT,"discovery":true,"upnp":false,"relay":"127.0.0.1:$RELAY_PORT"}
EOF

"$WORK/syncme-relay$EXE" -listen "127.0.0.1:$RELAY_PORT" > "$WORK/relay.log" 2>&1 &
"$SYNCME" daemon > "$WORK/daemon.log" 2>&1 &
for _ in $(seq 50); do "$SYNCME" status >/dev/null 2>&1 && break; sleep 0.1; done

yes y | "$SYNCME" pair > "$WORK/pair.log" 2>&1 &
for _ in $(seq 50); do grep -q "enter code" "$WORK/pair.log" 2>/dev/null && break; sleep 0.1; done
TOKEN=$(grep -o 'enter code: *[A-Z0-9]*' "$WORK/pair.log" | awk '{print $NF}')
echo "pairing token: $TOKEN"

cd "$ROOT/android/core-test"
status=0
"${GRADLE:-../gradlew}" -q run --args="$TOKEN $(native "$WORK/pc") $(native "$WORK/phone") $(native "$WORK/state") $PORT 127.0.0.1:$RELAY_PORT $(native "$SYNCME")" || status=$?

PC_CODE=$(grep -o 'Verification code: *[0-9 ]*' "$WORK/pair.log" | tr -dc '0-9')
PHONE_CODE=$(cat "$WORK/state/code.txt" 2>/dev/null || true)
if [ -n "$PC_CODE" ] && [ "$PC_CODE" = "$PHONE_CODE" ]; then
  echo "  ok   PC and phone show the same verification code ($PC_CODE)"
else
  echo "  FAIL verification codes differ: pc=$PC_CODE phone=$PHONE_CODE"; status=1
fi
"$SYNCME" status || true
echo "--- daemon log ---"; cat "$WORK/daemon.log"
echo "--- relay log ---"; cat "$WORK/relay.log"
exit $status
