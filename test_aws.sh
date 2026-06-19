#!/bin/bash
set -uo pipefail

BINARY="/tmp/agent-vm-aws-test"
CONFIG_DIR="$HOME/.config/agent-vm"
CONTAINER="aws-test-$$"
LS_CTR="localstack-$$"
BUCKET="agent-vm-test-$$"
TEST_CONTENT="hello-from-agent-vm-aws-$$"

echo "╔══════════════════════════════════════════════════╗"
echo "║  AWS Credential Forwarding + LocalStack E2E     ║"
echo "╚══════════════════════════════════════════════════╝"

# ── Build ──
echo "[1/9] Building agent-vm..."
go build -o "$BINARY" *.go || { echo "❌ Build failed"; exit 1; }

# ── Save existing configs ──
SAVED=""
if [ -f "$CONFIG_DIR/secrets.json" ]; then
    cp "$CONFIG_DIR/secrets.json" "$CONFIG_DIR/secrets.json.bak"
    SAVED="yes"
fi

# ── Create secrets.json with AWS credentials ──
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/secrets.json" << 'EOF'
{
  "credentials": {
    "https://github.com": { "username": "token", "secret": "ghp_aws_test_67890" }
  },
  "env": {
    "AWS_ACCESS_KEY_ID": "test",
    "AWS_SECRET_ACCESS_KEY": "test",
    "AWS_DEFAULT_REGION": "us-east-1"
  }
}
EOF

cleanup() {
    echo ""
    echo "[cleanup] Stopping LocalStack..."
    podman rm -f "$LS_CTR" 2>/dev/null || true
    echo "[cleanup] Removing test container..."
    "$BINARY" destroy "$CONTAINER" 2>/dev/null || true
    if [ -n "$SAVED" ]; then
        mv "$CONFIG_DIR/secrets.json.bak" "$CONFIG_DIR/secrets.json"
    else
        rm -f "$CONFIG_DIR/secrets.json"
    fi
    rm -f "$BINARY" "$CONFIG_DIR"/${CONTAINER}.* 2>/dev/null
}
trap cleanup EXIT

# ── Start container ──
echo "[2/9] Starting dev container with AWS credentials..."
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

# ── Test 1: AWS env vars injected ──
echo ""
echo "[3/9] Verify AWS credential env vars..."
exec_in 'echo "  AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID"'
exec_in 'echo "  AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY"'
exec_in 'echo "  AWS_DEFAULT_REGION=$AWS_DEFAULT_REGION"'
if exec_in 'test "$AWS_ACCESS_KEY_ID" = "test" && test -n "$AWS_SECRET_ACCESS_KEY"' 2>/dev/null; then
    echo "  ✅ PASS"
else
    echo "  ❌ FAIL"
fi

# ── Test 2: Git credential lookup ──
echo ""
echo "[4/9] Verify git credential lookup..."
RESULT=$(exec_in 'printf "protocol=https\nhost=github.com\n" | git-credential-agentvm 2>/dev/null')
if echo "$RESULT" | grep -q "ghp_aws_test_67890"; then
    echo "  ✅ PASS"
else
    echo "  ❌ FAIL"
fi

# ── Detect gateway ──
echo ""
echo "[5/9] Detecting gateway..."
GW=$(container exec -u vm "$CONTAINER" bash -c '
    HEX=$(awk "\$2==\"00000000\" {print \$3; exit}" /proc/net/route 2>/dev/null)
    [ -n "$HEX" ] && [ ${#HEX} -eq 8 ] && printf "%d.%d.%d.%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2}
' 2>&1)
echo "  Gateway: $GW"

# ── Start LocalStack on host ──
echo ""
echo "[6/9] Starting LocalStack (S3) on host..."
podman run -d --name "$LS_CTR" \
    -p 4566:4566 \
    -e SERVICES=s3 \
    docker.io/localstack/localstack:4.0 2>&1 || true

echo "  Waiting for LocalStack..."
LS_READY=false
for i in $(seq 1 40); do
    if curl -sf "http://localhost:4566/_localstack/health" >/dev/null 2>&1; then
        echo "  LocalStack ready (${i}s)"
        LS_READY=true
        break
    fi
    sleep 2
done
if [ "$LS_READY" = false ]; then
    echo "  ❌ LocalStack did not start"
    exit 1
fi

# ── Install boto3 in container ──
echo ""
echo "[7/9] Installing boto3 in container..."
exec_in 'uv pip install --system boto3 2>&1 | tail -3'

# ── Test 3: S3 round-trip ──
echo ""
echo "[8/9] S3 round-trip test (create bucket, put object, get object)..."
RESULT=$(exec_in "python3 -c \"
import boto3, sys

endpoint = 'http://$GW:4566'
s3 = boto3.client('s3', endpoint_url=endpoint)

# Create bucket
s3.create_bucket(Bucket='$BUCKET')
print('  bucket created: $BUCKET')

# Put object
s3.put_object(Bucket='$BUCKET', Key='hello.txt', Body=b'$TEST_CONTENT')
print('  object written: hello.txt')

# Get object
resp = s3.get_object(Bucket='$BUCKET', Key='hello.txt')
content = resp['Body'].read().decode()
print(f'  object read: {content}')

# List buckets
buckets = [b['Name'] for b in s3.list_buckets()['Buckets']]
print(f'  buckets: {buckets}')

if content == '$TEST_CONTENT':
    print('PASS')
else:
    print(f'FAIL: expected $TEST_CONTENT, got {content}')
    sys.exit(1)
\" 2>&1")

echo "$RESULT"
if echo "$RESULT" | grep -q "^PASS$"; then
    echo "  ✅ PASS: S3 round-trip via credential-forwarded container"
else
    echo "  ❌ FAIL"
fi

# ── Test 4: kcat also available for Kafka ──
echo ""
echo "[9/9] Bonus: verify kcat (Kafka client) still available..."
exec_in 'kcat --version 2>&1 | head -1'

echo ""
echo "══════════════════════════════════════════════════"
echo "Test complete."
