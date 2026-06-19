#!/bin/bash
set -uo pipefail

BINARY="/tmp/agent-vm-kafka-sasl-test"
CONFIG_DIR="$HOME/.config/agent-vm"
CONTAINER="kafka-sasl-$$"
KAFKA_CTR="kafka-sasl-host-$$"
CERT_DIR="/tmp/kafka-sasl-certs-$$"
TEST_MSG="sasl-ssl-hello-$$"
BUCKET_DIR="./.kafka-test-certs-$$"

echo "╔════════════════════════════════════════════════════╗"
echo "║  Kafka SASL_SSL + Credential Forwarding E2E       ║"
echo "╚════════════════════════════════════════════════════╝"

# ── Build ──
echo "[1/9] Building agent-vm..."
go build -o "$BINARY" *.go || { echo "❌ Build failed"; exit 1; }

# ── Save existing configs ──
SAVED=""
if [ -f "$CONFIG_DIR/secrets.json" ]; then
    cp "$CONFIG_DIR/secrets.json" "$CONFIG_DIR/secrets.json.bak"
    SAVED="yes"
fi

# ── Create secrets.json ──
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/secrets.json" << 'EOF'
{
  "env": {
    "KAFKA_BOOTSTRAP_SERVERS": "localhost:9093",
    "KAFKA_SASL_MECHANISM": "PLAIN",
    "KAFKA_SASL_USERNAME": "kafkauser",
    "KAFKA_SASL_PASSWORD": "kafka-secret-password",
    "KAFKA_SECURITY_PROTOCOL": "SASL_SSL"
  }
}
EOF

# ── Generate TLS certs ──
echo "[2/9] Generating TLS certificates..."
mkdir -p "$CERT_DIR"
openssl req -new -x509 -keyout "$CERT_DIR/ca-key.pem" -out "$CERT_DIR/ca-cert.pem" \
    -days 1 -subj "/CN=kafka-test-ca" -nodes 2>/dev/null
openssl req -new -keyout "$CERT_DIR/broker-key.pem" -out "$CERT_DIR/broker-req.pem" \
    -subj "/CN=localhost" -nodes 2>/dev/null
openssl x509 -req -CA "$CERT_DIR/ca-cert.pem" -CAkey "$CERT_DIR/ca-key.pem" \
    -in "$CERT_DIR/broker-req.pem" -out "$CERT_DIR/broker-cert.pem" -days 1 -CAcreateserial 2>/dev/null
cat "$CERT_DIR/broker-cert.pem" "$CERT_DIR/broker-key.pem" > "$CERT_DIR/broker.pem"
echo "  certs generated: $CERT_DIR"

# Also place CA cert in workspace so container can access it
mkdir -p "$BUCKET_DIR"
cp "$CERT_DIR/ca-cert.pem" "$BUCKET_DIR/ca-cert.pem"

cleanup() {
    echo ""
    echo "[cleanup] Removing Kafka..."
    podman rm -f "$KAFKA_CTR" 2>/dev/null || true
    echo "[cleanup] Removing container..."
    "$BINARY" destroy "$CONTAINER" 2>/dev/null || true
    if [ -n "$SAVED" ]; then
        mv "$CONFIG_DIR/secrets.json.bak" "$CONFIG_DIR/secrets.json"
    else
        rm -f "$CONFIG_DIR/secrets.json"
    fi
    rm -f "$BINARY" "$CONFIG_DIR"/${CONTAINER}.* 2>/dev/null
    rm -rf "$CERT_DIR" "$BUCKET_DIR"
}
trap cleanup EXIT

# ── Start container ──
echo "[3/9] Starting dev container..."
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

# ── Verify credentials ──
echo ""
echo "[4/9] Verify SASL credentials injected..."
exec_in 'echo "  KAFKA_SASL_USERNAME=$KAFKA_SASL_USERNAME"'
exec_in 'echo "  KAFKA_SASL_PASSWORD=$KAFKA_SASL_PASSWORD"'
exec_in 'echo "  KAFKA_SECURITY_PROTOCOL=$KAFKA_SECURITY_PROTOCOL"'
if exec_in 'test -n "$KAFKA_SASL_PASSWORD"' 2>/dev/null; then
    echo "  ✅ PASS"
else
    echo "  ❌ FAIL"; exit 1
fi

# ── Detect gateway ──
echo ""
echo "[5/9] Detecting gateway..."
GW=$(container exec -u vm "$CONTAINER" bash -c '
    HEX=$(awk "\$2==\"00000000\" {print \$3; exit}" /proc/net/route 2>/dev/null)
    [ -n "$HEX" ] && [ ${#HEX} -eq 8 ] && printf "%d.%d.%d.%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2}
' 2>&1)
echo "  Gateway: $GW"

# ── Verify CA cert accessible from container ──
echo ""
echo "[6/9] Verify CA cert in workspace..."
CA_IN_CONTAINER=$(exec_in "ls /home/vm/workspace/$BUCKET_DIR/ca-cert.pem 2>/dev/null && echo OK || echo MISSING")
echo "  CA cert: $CA_IN_CONTAINER"

# ── Start Kafka with SASL_SSL ──
echo ""
echo "[7/9] Starting Kafka with SASL_SSL on host..."
podman run -d --name "$KAFKA_CTR" \
    -p 9093:9093 \
    -v "$CERT_DIR:/etc/kafka/secrets:ro" \
    -e KAFKA_NODE_ID=1 \
    -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=SASL_SSL://:9093,CONTROLLER://:9094 \
    -e KAFKA_ADVERTISED_LISTENERS=SASL_SSL://$GW:9093 \
    -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9094 \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:SASL_SSL,SASL_SSL:SASL_SSL \
    -e KAFKA_SASL_ENABLED_MECHANISMS=PLAIN \
    -e KAFKA_LISTENER_NAME_CONTROLLER_SASL_ENABLED_MECHANISMS=PLAIN \
    -e KAFKA_INTER_BROKER_LISTENER_NAME=SASL_SSL \
    -e 'KAFKA_LISTENER_NAME_SASL_SSL_PLAIN_SASL_JAAS_CONFIG=org.apache.kafka.common.security.plain.PlainLoginModule required username="admin" password="admin-secret" user_kafkauser="kafka-secret-password";' \
    -e 'KAFKA_LISTENER_NAME_CONTROLLER_PLAIN_SASL_JAAS_CONFIG=org.apache.kafka.common.security.plain.PlainLoginModule required username="admin" password="admin-secret";' \
    -e KAFKA_SSL_KEYSTORE_TYPE=PEM \
    -e KAFKA_SSL_KEYSTORE_LOCATION=/etc/kafka/secrets/broker.pem \
    -e KAFKA_SSL_TRUSTSTORE_TYPE=PEM \
    -e KAFKA_SSL_TRUSTSTORE_LOCATION=/etc/kafka/secrets/ca-cert.pem \
    -e KAFKA_SSL_ENDPOINT_IDENTIFICATION_ALGORITHM= \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
    -e KAFKA_GROUP_INITIAL_REPLICA_DELAY=0 \
    -e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk \
    docker.io/apache/kafka:3.8.0 2>&1 || true

echo "  Waiting for Kafka SASL_SSL broker..."
KAFKA_READY=false
for i in $(seq 1 45); do
    if podman exec "$KAFKA_CTR" /opt/kafka/bin/kafka-topics.sh \
        --bootstrap-server localhost:9093 \
        --command-config <(echo "security.protocol=SASL_SSL"; echo "sasl.mechanism=PLAIN"; \
            echo 'sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="admin" password="admin-secret";'; \
            echo "ssl.truststore.location=/etc/kafka/secrets/ca-cert.pem"; echo "ssl.truststore.type=PEM"; \
            echo "ssl.endpoint.identification.algorithm=") \
        --list >/dev/null 2>&1; then
        echo "  Kafka ready (${i}s)"
        KAFKA_READY=true
        break
    fi
    sleep 2
done

# Simpler readiness check if the above fails
if [ "$KAFKA_READY" = false ]; then
    echo "  Trying simpler readiness check..."
    for i in $(seq 1 15); do
        if podman logs "$KAFKA_CTR" 2>&1 | grep -q "started"; then
            echo "  Kafka seems started (from logs, ${i}s)"
            KAFKA_READY=true
            break
        fi
        sleep 2
    done
fi

if [ "$KAFKA_READY" = false ]; then
    echo "  ❌ Kafka did not start"
    echo "  Last logs:"
    podman logs "$KAFKA_CTR" 2>&1 | tail -10
    exit 1
fi

# ── Produce from container via SASL_SSL ──
echo ""
echo "[8/9] Produce via SASL_SSL from container..."
KCAT=$(exec_in 'which kcat kafkacat 2>/dev/null | head -1')
CA_PATH="/home/vm/workspace/$BUCKET_DIR/ca-cert.pem"

echo "  $KCAT -b $GW:9093 -X security.protocol=SASL_SSL ..."
exec_in "echo '$TEST_MSG' | $KCAT \
    -b $GW:9093 \
    -X security.protocol=SASL_SSL \
    -X sasl.mechanism=PLAIN \
    -X sasl.username=\$KAFKA_SASL_USERNAME \
    -X sasl.password=\$KAFKA_SASL_PASSWORD \
    -X ssl.ca.location=$CA_PATH \
    -X ssl.endpoint.identification.algorithm= \
    -t sasl-test -P -e 2>&1" || true

# ── Consume from container via SASL_SSL ──
echo ""
echo "[9/9] Consume via SASL_SSL from container..."
CONSUMED=$(exec_in "$KCAT \
    -b $GW:9093 \
    -X security.protocol=SASL_SSL \
    -X sasl.mechanism=PLAIN \
    -X sasl.username=\$KAFKA_SASL_USERNAME \
    -X sasl.password=\$KAFKA_SASL_PASSWORD \
    -X ssl.ca.location=$CA_PATH \
    -X ssl.endpoint.identification.algorithm= \
    -t sasl-test -C -e -o beginning -q 2>/dev/null" || true)
echo "  received: '$CONSUMED'"

if echo "$CONSUMED" | grep -q "$TEST_MSG"; then
    echo "  ✅ PASS: Kafka SASL_SSL produce/consume with forwarded credentials"
else
    echo "  ❌ FAIL"
fi

echo ""
echo "════════════════════════════════════════════════════"
echo "Test complete."
