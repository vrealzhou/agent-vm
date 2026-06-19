#!/bin/bash
set -uo pipefail

BINARY="/tmp/agent-vm-test"
CONFIG_DIR="$HOME/.config/agent-vm"
PROXY_JSON="$CONFIG_DIR/proxy.json"
CONTAINER="proxy-test-$$"

echo "╔══════════════════════════════════════════════╗"
echo "║  MITM Proxy End-to-End Test                  ║"
echo "╚══════════════════════════════════════════════╝"
echo ""

# ── Build ──
echo "[1/6] Building agent-vm..."
go build -o "$BINARY" *.go || { echo "❌ Build failed"; exit 1; }

# ── Save existing proxy.json ──
SAVED=""
if [ -f "$PROXY_JSON" ]; then
    cp "$PROXY_JSON" "$PROXY_JSON.test-bak"
    SAVED="yes"
fi

# ── Create test proxy.json ──
mkdir -p "$CONFIG_DIR"
cat > "$PROXY_JSON" << 'EOF'
{
  "credentials": {
    "postman-echo.com": {
      "X-Proxy-Injected": "secret-from-host-12345"
    }
  },
  "whitelist": [
    "postman-echo.com"
  ]
}
EOF

cleanup() {
    echo ""
    echo "[cleanup] Removing test container..."
    "$BINARY" destroy "$CONTAINER" 2>/dev/null || true
    if [ -n "$SAVED" ]; then
        mv "$PROXY_JSON.test-bak" "$PROXY_JSON"
    else
        rm -f "$PROXY_JSON"
    fi
    rm -f "$BINARY" "$CONFIG_DIR"/*.proxy.pid
}
trap cleanup EXIT

# ── Start container with proxy ──
echo "[2/6] Starting container '$CONTAINER' with proxy..."
"$BINARY" start "$CONTAINER" -w . -d 2>&1
echo "Waiting for socat bridge..."
for i in $(seq 1 20); do
    if container exec -u vm "$CONTAINER" bash -c 'echo > /dev/tcp/127.0.0.1/18080' 2>/dev/null; then
        echo "  socat bridge ready (after ${i}s)"
        break
    fi
    sleep 1
done

# Helper: run command inside container as vm user
exec_in() {
    container exec -u vm "$CONTAINER" bash -c "source ~/.dev-tools.sh 2>/dev/null; $1" 2>&1
}

# ── Diagnostics ──
echo ""
echo "[3/6] Diagnostics..."
echo "  Proxy env:"
exec_in 'echo "    HTTP_PROXY=$HTTP_PROXY"; echo "    HTTPS_PROXY=$HTTPS_PROXY"; echo "    NO_PROXY=$NO_PROXY"'
echo "  CA cert:"
exec_in 'ls /usr/local/share/ca-certificates/agent-vm-proxy-ca.crt 2>/dev/null && echo "    ✅ installed" || echo "    ❌ missing"'
echo "  socat bridge:"
exec_in 'pgrep -a socat 2>/dev/null | head -1 || echo "    not running"'
echo "  Gateway:"
exec_in 'ip route show default 2>/dev/null | awk "{print \"    gateway: \"\$3}" || echo "    unknown"'

# ── Test 1: Header injection ──
echo ""
echo "[4/6] Test: HTTPS header injection (MITM)"
echo "  curl https://postman-echo.com/headers → expect X-Proxy-Injected in response"
PASS4=false
for attempt in 1 2 3; do
    RESULT=$(exec_in 'curl -s --max-time 15 https://postman-echo.com/headers' || echo "CURL_FAILED")
    if echo "$RESULT" | grep -q "secret-from-host-12345"; then
        echo "  ✅ PASS (attempt $attempt): Header injected"
        echo "  Response: $RESULT"
        PASS4=true
        break
    fi
    echo "  attempt $attempt: $(echo "$RESULT" | head -1)"
    sleep 2
done
if [ "$PASS4" = false ]; then
    echo "  ❌ FAIL: Header not injected after 3 attempts"
fi

# ── Test 2: Whitelist blocking ──
echo ""
echo "[5/6] Test: Whitelist blocking"
echo "  curl https://example.com → expect blocked (not in whitelist)"
RESULT=$(exec_in 'curl -s -o /dev/null -w "%{http_code}" --max-time 8 https://example.com' 2>&1 || true)
echo "  HTTP status: $RESULT"
if echo "$RESULT" | grep -qE '^(000|403|000000)$'; then
    echo "  ✅ PASS: Non-whitelisted domain blocked"
else
    echo "  ⚠️  WARNING: Domain accessible (status $RESULT) — whitelist may not be enforced"
fi

# ── Test 3: Whitelisted domain works ──
echo ""
echo "[6/6] Test: Whitelisted domain accessible"
echo "  curl https://postman-echo.com/get → expect 200"
RESULT=$(exec_in 'curl -s -o /dev/null -w "%{http_code}" --max-time 10 https://postman-echo.com/get' 2>&1 || echo "000")
echo "  HTTP status: $RESULT"
if [ "$RESULT" = "200" ]; then
    echo "  ✅ PASS: Whitelisted domain accessible"
else
    echo "  ❌ FAIL: Whitelisted domain not accessible (status $RESULT)"
fi

echo ""
echo "════════════════════════════════════════════════"
echo "Test complete."
echo ""
echo "=== Proxy daemon log ==="
cat "$CONFIG_DIR/$CONTAINER.proxy.log" 2>/dev/null || echo "(no log)"
