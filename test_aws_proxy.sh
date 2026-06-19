#!/bin/bash
set -uo pipefail

BINARY="/tmp/agent-vm-aws-proxy-test"
CONFIG_DIR="$HOME/.config/agent-vm"
CONTAINER="aws-proxy-test-$$"

echo "╔════════════════════════════════════════════════════╗"
echo "║  AWS SigV4 MITM Proxy — HTTPS Rewrite E2E         ║"
echo "╚════════════════════════════════════════════════════╝"

# ── Build ──
echo "[1/6] Building agent-vm..."
go build -o "$BINARY" *.go || { echo "❌ Build failed"; exit 1; }

# ── Save existing proxy.json ──
SAVED=""
if [ -f "$CONFIG_DIR/proxy.json" ]; then
    cp "$CONFIG_DIR/proxy.json" "$CONFIG_DIR/proxy.json.bak"
    SAVED="yes"
fi

# ── Clean CA keys so they regenerate ──
rm -f "$CONFIG_DIR/proxy-ca.crt" "$CONFIG_DIR/proxy-ca.key"

# ── Create proxy.json with aws-sigv4 provider ──
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/proxy.json" << 'EOF'
{
  "providers": {
    "postman-echo.com": {
      "type": "aws-sigv4",
      "config": {
        "access_key": "AKIAIOSFODNN7EXAMPLE",
        "secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
        "region": "us-east-1",
        "service": "execute-api"
      }
    }
  },
  "whitelist": ["postman-echo.com"]
}
EOF

cleanup() {
    echo ""
    echo "[cleanup] Removing container..."
    "$BINARY" destroy "$CONTAINER" 2>/dev/null || true
    if [ -n "$SAVED" ]; then
        mv "$CONFIG_DIR/proxy.json.bak" "$CONFIG_DIR/proxy.json"
    else
        rm -f "$CONFIG_DIR/proxy.json"
    fi
    rm -f "$BINARY" "$CONFIG_DIR"/${CONTAINER}.* 2>/dev/null
}
trap cleanup EXIT

# ── Start container with proxy ──
echo "[2/6] Starting container with MITM proxy (aws-sigv4 provider)..."
"$BINARY" start "$CONTAINER" -w . -d 2>&1
echo "Waiting for socat bridge..."
for i in $(seq 1 20); do
    if container exec -u vm "$CONTAINER" bash -c 'echo > /dev/tcp/127.0.0.1/18080' 2>/dev/null; then
        echo "  bridge ready (${i}s)"
        break
    fi
    sleep 1
done

exec_in() {
    container exec -u vm "$CONTAINER" bash -c "source ~/.dev-tools.sh 2>/dev/null; $1" 2>&1
}

# ── Verify proxy env ──
echo ""
echo "[3/6] Verify proxy environment..."
exec_in 'echo "  HTTP_PROXY=$HTTP_PROXY"'
exec_in 'ls /usr/local/share/ca-certificates/agent-vm-proxy-ca.crt >/dev/null 2>&1 && echo "  CA cert: ✅" || echo "  CA cert: ❌"'

# ── Test: AWS SigV4 header injection ──
echo ""
echo "[4/6] Test: HTTPS request gets AWS SigV4 signature via MITM"
echo "  curl -s https://postman-echo.com/headers (no auth — proxy signs it)"

PASS=false
for attempt in 1 2 3 4 5; do
    RESULT=$(exec_in 'curl -s --max-time 15 https://postman-echo.com/headers' || echo "CURL_FAILED")
    
    if echo "$RESULT" | grep -q "AWS4-HMAC-SHA256"; then
        echo "  ✅ PASS (attempt $attempt): SigV4 Authorization header injected"
        
        # Extract and display the auth header
        AUTH=$(echo "$RESULT" | grep -o '"authorization":"[^"]*"' | head -1)
        echo "  $AUTH"
        
        # Verify signature components
        if echo "$RESULT" | grep -q "AKIAIOSFODNN7EXAMPLE"; then
            echo "  ✅ Access key in Credential"
        fi
        if echo "$RESULT" | grep -q "us-east-1"; then
            echo "  ✅ Region in Credential scope"
        fi
        if echo "$RESULT" | grep -q "Signature="; then
            echo "  ✅ Signature present"
        fi
        
        # Also check X-Amz-Date was added
        if echo "$RESULT" | grep -qi "x-amz-date"; then
            echo "  ✅ X-Amz-Date header present"
        fi
        
        PASS=true
        break
    fi
    
    SHORT=$(echo "$RESULT" | head -c 80)
    echo "  attempt $attempt: $SHORT"
    sleep 3
done

if [ "$PASS" = false ]; then
    echo "  ❌ FAIL: SigV4 header not found after 5 attempts"
    
    # Print proxy log for debugging
    echo ""
    echo "  Proxy daemon log:"
    cat "$CONFIG_DIR/$CONTAINER.proxy.log" 2>/dev/null | tail -15
fi

# ── Test: Request without proxy would NOT have auth ──
echo ""
echo "[5/6] Verify: same request WITHOUT proxy has no auth header"
# Use a direct curl (bypass proxy) to show the difference
DIRECT=$(exec_in 'curl -s --max-time 10 --noproxy "*" https://postman-echo.com/headers 2>/dev/null || echo "DIRECT_FAILED"')
if echo "$DIRECT" | grep -q "AWS4-HMAC-SHA256"; then
    echo "  ⚠️  Unexpected: auth present without proxy"
elif echo "$DIRECT" | grep -q "headers"; then
    echo "  ✅ Confirmed: no auth header without proxy"
else
    echo "  (direct request result: $DIRECT)"
fi

# ── Summary ──
echo ""
echo "[6/6] Summary"
echo "  Proxy.json configured: aws-sigv4 provider for postman-echo.com"
echo "  Credentials: AKIAIOSFODNN7EXAMPLE (test key, not real)"
echo "  The proxy intercepts HTTPS via MITM, signs the request with"
echo "  AWS SigV4, and forwards it. The target sees a valid-looking"
echo "  Authorization header without the container ever having the keys."

echo ""
echo "════════════════════════════════════════════════════"
echo "Test complete."
