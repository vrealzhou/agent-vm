#!/bin/bash
set -uo pipefail

BINARY="/tmp/agent-vm-kafka-test"
CONFIG_DIR="$HOME/.config/agent-vm"
CONTAINER="kafka-test-$$"
KAFKA_HOST_CTR="kafka-host-$$"
TEST_MSG="hello-from-agent-vm-$$"

echo "╔══════════════════════════════════════════════════╗"
echo "║  Kafka + Credential Forwarding E2E Test         ║"
echo "╚══════════════════════════════════════════════════╝"

# ── Build ──
echo "[1/8] Building agent-vm..."
go build -o "$BINARY" *.go || { echo "❌ Build failed"; exit 1; }

# ── Save existing configs ──
SAVED_SECRETS=""
if [ -f "$CONFIG_DIR/secrets.json" ]; then
    cp "$CONFIG_DIR/secrets.json" "$CONFIG_DIR/secrets.json.bak"
    SAVED_SECRETS="yes"
fi

# ── Create secrets.json with Kafka credentials ──
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/secrets.json" << EOF
{
  "credentials": {
    "https://github.com": {
      "username": "token",
      "secret": "ghp_test_kafka_12345"
    }
  },
  "env": {
    "KAFKA_BOOTSTRAP_SERVERS": "localhost:9092",
    "KAFKA_SASL_MECHANISM": "PLAIN",
    "KAFKA_SASL_USERNAME": "kafkauser",
    "KAFKA_SASL_PASSWORD": "kafka-secret-password"
  }
}
EOF

cleanup() {
    echo ""
    echo "[cleanup] Removing Kafka host container..."
    podman rm -f "$KAFKA_HOST_CTR" 2>/dev/null || true
    echo "[cleanup] Removing test container..."
    "$BINARY" destroy "$CONTAINER" 2>/dev/null || true
    if [ -n "$SAVED_SECRETS" ]; then
        mv "$CONFIG_DIR/secrets.json.bak" "$CONFIG_DIR/secrets.json"
    else
        rm -f "$CONFIG_DIR/secrets.json"
    fi
    rm -f "$BINARY" "$CONFIG_DIR"/${CONTAINER}.* 2>/dev/null
}
trap cleanup EXIT

# ── Start container ──
echo "[2/8] Starting dev container with credentials..."
"$BINARY" start "$CONTAINER" -w . -d --no-proxy 2>&1
echo "Waiting for credential bridge..."
for i in $(seq 1 20); do
    if container exec -u vm "$CONTAINER" bash -c 'echo > /dev/tcp/127.0.0.1/18081' 2>/dev/null; then
        echo "  bridge ready (${i}s)"
        break
    fi
    sleep 1
done

exec_in() {
    container exec -u vm "$CONTAINER" bash -c "source ~/.dev-tools.sh 2>/dev/null; $1" 2>&1
}

# ── Test 1: Credentials injected ──
echo ""
echo "[3/8] Verify credential env vars..."
exec_in 'echo "  KAFKA_BOOTSTRAP_SERVERS=$KAFKA_BOOTSTRAP_SERVERS"'
exec_in 'echo "  KAFKA_SASL_USERNAME=$KAFKA_SASL_USERNAME"'
exec_in 'echo "  KAFKA_SASL_PASSWORD=$KAFKA_SASL_PASSWORD"'
if exec_in 'test -n "$KAFKA_SASL_PASSWORD"' 2>/dev/null; then
    echo "  ✅ PASS"
else
    echo "  ❌ FAIL"
fi

# ── Test 2: Git credential helper ──
echo ""
echo "[4/8] Verify git credential lookup..."
RESULT=$(exec_in 'printf "protocol=https\nhost=github.com\n" | git-credential-agentvm 2>/dev/null')
if echo "$RESULT" | grep -q "ghp_test_kafka_12345"; then
    echo "  ✅ PASS"
else
    echo "  ❌ FAIL: $RESULT"
fi

# ── Detect gateway IP (host reachable from container) ──
echo ""
echo "[5/8] Detecting gateway..."
GW=$(container exec -u vm "$CONTAINER" bash -c '
    HEX=$(awk "\$2==\"00000000\" {print \$3; exit}" /proc/net/route 2>/dev/null)
    if [ -n "$HEX" ] && [ ${#HEX} -eq 8 ]; then
        printf "%d.%d.%d.%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2}
    fi
' 2>&1)
echo "  Gateway: $GW"

if [ -z "$GW" ]; then
    echo "  ❌ Cannot detect gateway — aborting Kafka test"
    exit 1
fi

# ── Start Kafka on host ──
echo ""
echo "[6/8] Starting Kafka on host (podman, advertised=$GW:9092)..."
podman run -d --name "$KAFKA_HOST_CTR" \
    -p 9092:9092 \
    -e KAFKA_NODE_ID=1 \
    -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
    -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://$GW:9092 \
    -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
    -e KAFKA_GROUP_INITIAL_REPLICA_DELAY=0 \
    -e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk \
    docker.io/apache/kafka:3.8.0 2>&1 || true

echo "  Waiting for Kafka broker..."
KAFKA_READY=false
for i in $(seq 1 30); do
    if podman exec "$KAFKA_HOST_CTR" /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list >/dev/null 2>&1; then
        echo "  Kafka ready (${i}s)"
        KAFKA_READY=true
        break
    fi
    sleep 2
done

if [ "$KAFKA_READY" = false ]; then
    echo "  ❌ Kafka did not start in time"
    exit 1
fi

# Create test topic
podman exec "$KAFKA_HOST_CTR" /opt/kafka/bin/kafka-topics.sh \
    --create --topic e2e-test --bootstrap-server localhost:9092 --if-not-exists 2>&1 || true

# ── Test 3: Produce from container ──
echo ""
echo "[7/8] Produce message from container..."
KCAT_BIN=$(exec_in 'which kcat kafkacat 2>/dev/null | head -1 || echo ""')
echo "  kcat binary: ${KCAT_BIN:-not found}"
if [ -z "$KCAT_BIN" ]; then
    echo "  ❌ kcat not installed — check Dockerfile"
    exit 1
fi
echo "  $KCAT_BIN -b $GW:9092 -t e2e-test -P -e"
exec_in "echo '$TEST_MSG' | $KCAT_BIN -b $GW:9092 -t e2e-test -P -e 2>&1" || true

# ── Test 4: Consume from container ──
echo ""
echo "[8/8] Consume message from container..."
echo "  $KCAT_BIN -b $GW:9092 -t e2e-test -C -e -o beginning"
CONSUMED=$(exec_in "$KCAT_BIN -b $GW:9092 -t e2e-test -C -e -o beginning -q 2>/dev/null" || true)
echo "  received: '$CONSUMED'"

if echo "$CONSUMED" | grep -q "$TEST_MSG"; then
    echo "  ✅ PASS: Kafka produce/consume via credential-forwarded container"
else
    echo "  ❌ FAIL: message not received"
fi

echo ""
echo "══════════════════════════════════════════════════"
echo "Test complete."
