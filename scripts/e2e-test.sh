#!/usr/bin/env bash
# End-to-end test: Go daemon + relay vs. the Android app's Kotlin core (on the JVM).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)
WORK=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT
PORT=47900
RELAY_PORT=47912

go build -o "$WORK/syncme" ./cmd/syncme
go build -o "$WORK/syncme-relay" ./cmd/syncme-relay

export SYNCME_HOME="$WORK/home"
mkdir -p "$SYNCME_HOME/config" "$WORK/pc"
cat > "$SYNCME_HOME/config/config.json" <<EOF
{"name":"Test PC","folder":"$WORK/pc","port":$PORT,"discovery":true,"upnp":false,"relay":"127.0.0.1:$RELAY_PORT"}
EOF

"$WORK/syncme-relay" -listen "127.0.0.1:$RELAY_PORT" > "$WORK/relay.log" 2>&1 &
"$WORK/syncme" daemon > "$WORK/daemon.log" 2>&1 &
for _ in $(seq 50); do "$WORK/syncme" status >/dev/null 2>&1 && break; sleep 0.1; done

yes y | "$WORK/syncme" pair > "$WORK/pair.log" 2>&1 &
for _ in $(seq 50); do grep -q "enter code" "$WORK/pair.log" 2>/dev/null && break; sleep 0.1; done
TOKEN=$(grep -o 'enter code: *[A-Z0-9]*' "$WORK/pair.log" | awk '{print $NF}')
echo "pairing token: $TOKEN"

cd "$ROOT/android/core-test"
status=0
"${GRADLE:-../gradlew}" -q run --args="$TOKEN $WORK/pc $WORK/phone $WORK/state $PORT 127.0.0.1:$RELAY_PORT" || status=$?

PC_CODE=$(grep -o 'Verification code: *[0-9 ]*' "$WORK/pair.log" | tr -dc '0-9')
PHONE_CODE=$(cat "$WORK/state/code.txt" 2>/dev/null || true)
if [ -n "$PC_CODE" ] && [ "$PC_CODE" = "$PHONE_CODE" ]; then
  echo "  ok   PC and phone show the same verification code ($PC_CODE)"
else
  echo "  FAIL verification codes differ: pc=$PC_CODE phone=$PHONE_CODE"; status=1
fi
"$WORK/syncme" status || true
echo "--- daemon log ---"; cat "$WORK/daemon.log"
echo "--- relay log ---"; cat "$WORK/relay.log"
exit $status
