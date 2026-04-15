#!/bin/bash
# End-to-end test: echo ← webrtc-forward (via coordinator) ← cli-client
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ECHO="$DIR/echo/echo"
FORWARD="$DIR/webrtc-forward"
CLIENT="$DIR/cli-client/cli-client"
COORD="$DIR/coordinator/coordinator"

ECHO_ADDR="127.0.0.1:19777"
COORD_ADDR="127.0.0.1:19765"

TMPDIR=$(mktemp -d)
cleanup() {
    kill $(jobs -p) 2>/dev/null || true
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

wait_port() {
    local port=$1
    for i in $(seq 1 30); do
        (echo "" > /dev/tcp/127.0.0.1/$port) 2>/dev/null && return 0
        sleep 0.2
    done
    echo "FATAL: port $port never opened"
    return 1
}

wait_service() {
    local coord_base=$1
    local svc=$2
    echo "    waiting for service $svc to appear at coordinator..."
    for i in $(seq 1 30); do
        result=$(curl -sf "$coord_base/services" 2>/dev/null || true)
        if echo "$result" | grep -q "\"$svc\""; then
            echo "    service $svc registered"
            return 0
        fi
        sleep 0.3
    done
    echo "FATAL: service $svc never registered with coordinator"
    return 1
}

echo "==> Building binaries"
export PATH=$PATH:/usr/local/go/bin
(cd "$DIR" && go build -o "$FORWARD" . && go build -o "$CLIENT" ./cli-client && go build -o "$ECHO" ./echo && go build -o "$COORD" ./coordinator)

echo "==> Writing echo config"
cat > "$TMPDIR/echo.json5" <<EOF
{
  addr: "$ECHO_ADDR",
}
EOF

echo "==> Starting echo server on $ECHO_ADDR"
"$ECHO" run --config "$TMPDIR/echo.json5" >"$TMPDIR/echo.log" 2>&1 &
wait_port 19777 || { cat "$TMPDIR/echo.log"; exit 1; }
echo "    echo server up"

echo "==> Writing coordinator config"
cat > "$TMPDIR/coord.json5" <<EOF
{
  addr: "$COORD_ADDR",
}
EOF

echo "==> Starting coordinator on $COORD_ADDR"
"$COORD" run --config "$TMPDIR/coord.json5" >"$TMPDIR/coord.log" 2>&1 &
wait_port 19765 || { cat "$TMPDIR/coord.log"; exit 1; }
echo "    coordinator up"

echo "==> Writing webrtc-forward config with coordinator"
# Generate a fresh key via init, then extract it
"$FORWARD" init --config "$TMPDIR/fwd.json5" >/dev/null
# Rebuild config with coordinator URL (key line looks like:  key: "base64...",)
KEY=$(grep '^\s*key:' "$TMPDIR/fwd.json5" | sed 's/.*key: "\(.*\)".*/\1/')
cat > "$TMPDIR/fwd.json5" <<EOF
{
  services: {
    echo: "$ECHO_ADDR",
  },
  coordinators: ["ws://$COORD_ADDR/ws"],
  key: "$KEY",
}
EOF

echo "==> Starting webrtc-forward (coordinator mode)"
"$FORWARD" run --config "$TMPDIR/fwd.json5" >"$TMPDIR/forward.log" 2>&1 &
wait_service "http://$COORD_ADDR" "echo"

echo "==> Running cli-client via coordinator"
"$CLIENT" --coordinator "http://$COORD_ADDR" --service echo --messages "hello,ping,goodbye" 2>&1 | tee "$TMPDIR/client.log"

echo ""
echo "--- echo log ---"
cat "$TMPDIR/echo.log"
echo "--- forward log ---"
cat "$TMPDIR/forward.log"
echo "--- coord log ---"
cat "$TMPDIR/coord.log"
