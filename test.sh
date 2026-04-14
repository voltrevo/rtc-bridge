#!/bin/bash
# End-to-end test: echo server ← webrtc-forward ← cli-client
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ECHO="$DIR/echo/echo"
FORWARD="$DIR/webrtc-forward"
CLIENT="$DIR/cli-client/cli-client"

ECHO_ADDR="127.0.0.1:17777"
SIGNAL_ADDR="127.0.0.1:18765"

TMPDIR=$(mktemp -d)
cleanup() {
    kill $(jobs -p) 2>/dev/null || true
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

echo "==> Starting echo server on $ECHO_ADDR"
"$ECHO" "$ECHO_ADDR" >"$TMPDIR/echo.log" 2>&1 &

wait_port() {
    local port=$1
    for i in $(seq 1 30); do
        (echo "" > /dev/tcp/127.0.0.1/$port) 2>/dev/null && return 0
        sleep 0.2
    done
    return 1
}

# Wait for echo server to be up.
wait_port 17777 || { echo "FATAL: echo server not up"; cat "$TMPDIR/echo.log"; exit 1; }
echo "    echo server up"

echo "==> Starting webrtc-forward (--signal $SIGNAL_ADDR -> $ECHO_ADDR)"
"$FORWARD" --signal "$SIGNAL_ADDR" "$ECHO_ADDR" >"$TMPDIR/forward.log" 2>&1 &

# Wait for signal server to be up.
wait_port 18765 || { echo "FATAL: signal server not up"; cat "$TMPDIR/forward.log"; exit 1; }
echo "    webrtc-forward up"

echo "==> Running cli-client"
"$CLIENT" --signal "http://$SIGNAL_ADDR" --messages "hello,ping,goodbye" 2>&1 | tee "$TMPDIR/client.log"

echo ""
echo "--- echo log ---"
cat "$TMPDIR/echo.log"
echo "--- forward log ---"
cat "$TMPDIR/forward.log"
