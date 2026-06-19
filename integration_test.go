//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
	"github.com/vrealzhou/agent-vm/internal/container"
	"github.com/vrealzhou/agent-vm/internal/secrets"
)

const testEchoHost = "postman-echo.com"

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent-vm")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return bin
}

func execIn(t *testing.T, name, cmd string) string {
	t.Helper()
	out, _ := exec.Command("container", "exec", "-u", "vm", name,
		"bash", "-c", "source ~/.dev-tools.sh 2>/dev/null; "+cmd).CombinedOutput()
	return string(out)
}

func waitForBridge(t *testing.T, name string, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := execIn(t, name, fmt.Sprintf("echo > /dev/tcp/127.0.0.1/%d 2>/dev/null && echo OK", port))
		if strings.Contains(out, "OK") {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("bridge on port %d not ready after %v", port, timeout)
}

func detectGateway(t *testing.T, name string) string {
	t.Helper()
	out := execIn(t, name, `HEX=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route 2>/dev/null)
[ -n "$HEX" ] && [ ${#HEX} -eq 8 ] && printf "%d.%d.%d.%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2}`)
	gw := strings.TrimSpace(out)
	if gw == "" {
		t.Fatal("could not detect gateway IP")
	}
	return gw
}

func newContainerName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%100000)
}

func saveAndWriteConfig(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	saved := false
	if data, err := os.ReadFile(path); err == nil {
		os.WriteFile(path+".itest-bak", data, 0o644)
		saved = true
	}
	os.WriteFile(path, []byte(content), 0o644)
	t.Cleanup(func() {
		if saved {
			data, _ := os.ReadFile(path + ".itest-bak")
			os.WriteFile(path, data, 0o644)
			os.Remove(path + ".itest-bak")
		} else {
			os.Remove(path)
		}
	})
}

func setupContainer(t *testing.T, name, proxyYAML, secretsYAML string, useProxy bool) string {
	t.Helper()
	binary := buildBinary(t)

	os.Remove(config.CACertPath())
	os.Remove(config.CAKeyPath())

	if proxyYAML != "" {
		saveAndWriteConfig(t, config.ProxyConfigPath(), proxyYAML)
	}
	if secretsYAML != "" {
		saveAndWriteConfig(t, config.SecretsConfigPath(), secretsYAML)
	}

	// Start via CLI binary
	args := []string{binary, "start", name, "-w", ".", "-d"}
	if !useProxy {
		args = append(args, "--no-proxy")
	}
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("start container: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		exec.Command(binary, "destroy", name).Run()
		for _, suffix := range []string{".proxy.log", ".proxy.pid", ".cred.log", ".cred.pid", ".kafka.log", ".kafka.pid"} {
			os.Remove(filepath.Join(config.StateDir(), name+suffix))
		}
	})

	return binary
}

// ── MITM Proxy tests ──

func TestProxyMITMHeaderInjection(t *testing.T) {
	container := newContainerName("itest-mitm-")
	proxyYAML := fmt.Sprintf(`credentials:
  %s:
    X-Proxy-Injected: secret-from-host-12345
whitelist:
  - %s
`, testEchoHost, testEchoHost)
	setupContainer(t, container, proxyYAML, "", true)
	waitForBridge(t, container, 18080, 30*time.Second)

	for attempt := 1; attempt <= 5; attempt++ {
		body := execIn(t, container, fmt.Sprintf("curl -s --max-time 15 https://%s/headers", testEchoHost))
		if strings.Contains(body, "secret-from-host-12345") {
			t.Logf("✅ header injected on attempt %d", attempt)
			return
		}
		t.Logf("attempt %d failed", attempt)
		time.Sleep(2 * time.Second)
	}
	t.Fatal("X-Proxy-Injected not found after 5 attempts")
}

func TestProxyWhitelistBlocking(t *testing.T) {
	container := newContainerName("itest-wl-")
	proxyYAML := fmt.Sprintf(`whitelist:
  - %s
`, testEchoHost)
	setupContainer(t, container, proxyYAML, "", true)
	waitForBridge(t, container, 18080, 30*time.Second)

	status := strings.TrimSpace(execIn(t, container, "curl -s -o /dev/null -w '%{http_code}' --max-time 8 https://example.com"))
	if status == "000" || status == "403" {
		t.Logf("✅ blocked (status %s)", status)
		return
	}
	t.Errorf("expected blocked, got status %s", status)
}

func TestProxyAWSSigV4(t *testing.T) {
	container := newContainerName("itest-aws-")
	proxyYAML := `providers:
  postman-echo.com:
    type: aws-sigv4
    config:
      access_key: AKIAIOSFODNN7EXAMPLE
      secret_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
      region: us-east-1
      service: execute-api
whitelist:
  - postman-echo.com
`
	setupContainer(t, container, proxyYAML, "", true)
	waitForBridge(t, container, 18080, 30*time.Second)

	for attempt := 1; attempt <= 5; attempt++ {
		body := execIn(t, container, "curl -s --max-time 15 https://postman-echo.com/headers")
		if strings.Contains(body, "AWS4-HMAC-SHA256") {
			t.Logf("✅ SigV4 injected (attempt %d)", attempt)
			return
		}
		t.Logf("attempt %d failed", attempt)
		time.Sleep(3 * time.Second)
	}
	t.Fatal("AWS4-HMAC-SHA256 not found after 5 attempts")
}

// ── Credential forwarding tests ──

func TestCredentialEnvForwarding(t *testing.T) {
	container := newContainerName("itest-cred-")
	secretsYAML := `env:
  AWS_ACCESS_KEY_ID: test-key-123
  KAFKA_SASL_PASSWORD: kafka-secret-pwd
`
	setupContainer(t, container, "", secretsYAML, false)
	waitForBridge(t, container, 18081, 20*time.Second)

	out := execIn(t, container, `echo "AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID KAFKA_SASL_PASSWORD=$KAFKA_SASL_PASSWORD"`)
	if strings.Contains(out, "test-key-123") && strings.Contains(out, "kafka-secret-pwd") {
		t.Logf("✅ %s", strings.TrimSpace(out))
		return
	}
	t.Fatalf("env vars not forwarded:\n%s", out)
}

func TestCredentialGitHelper(t *testing.T) {
	container := newContainerName("itest-git-")
	secretsYAML := `credentials:
  https://github.com:
    username: token
    secret: ghp_integration_test_999
`
	setupContainer(t, container, "", secretsYAML, false)
	waitForBridge(t, container, 18081, 20*time.Second)

	result := execIn(t, container, `printf "protocol=https\nhost=github.com\n" | git-credential-agentvm 2>/dev/null`)
	if strings.Contains(result, "ghp_integration_test_999") {
		t.Log("✅ git credential retrieved from host")
		return
	}
	t.Fatalf("git credential not retrieved:\n%s", result)
}

// ── Secrets placeholder test ──

func TestSecretsPlaceholderWorkflow(t *testing.T) {
	binary := buildBinary(t)

	// Save existing secrets
	saveAndWriteConfig(t, config.SecretsConfigPath(), "")

	// Add placeholder
	out, err := exec.Command(binary, "secrets", "add", "test-aws",
		"--type", "aws-sigv4",
		"--field", "access_key=AKIAIOSFODNN7EXAMPLE",
		"--field", "secret_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"--field", "region=us-east-1",
		"--field", "service=s3",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("secrets add: %v\n%s", err, out)
	}

	// List
	out, _ = exec.Command(binary, "secrets", "list").CombinedOutput()
	if !strings.Contains(string(out), "test-aws") {
		t.Fatalf("secrets list missing test-aws:\n%s", out)
	}

	// Show (masked)
	out, _ = exec.Command(binary, "secrets", "show", "test-aws").CombinedOutput()
	if !strings.Contains(string(out), "****") {
		t.Errorf("masked show should hide secret:\n%s", out)
	}
	if strings.Contains(string(out), "wJalrXUtnFEMI") {
		t.Errorf("masked show should not reveal secret key:\n%s", out)
	}

	// Show (revealed)
	out, _ = exec.Command(binary, "secrets", "show", "test-aws", "--reveal").CombinedOutput()
	if !strings.Contains(string(out), "wJalrXUtnFEMI") {
		t.Errorf("revealed show should show secret key:\n%s", out)
	}

	// Remove
	out, _ = exec.Command(binary, "secrets", "remove", "test-aws").CombinedOutput()

	// Verify removed
	out, _ = exec.Command(binary, "secrets", "list").CombinedOutput()
	if strings.Contains(string(out), "test-aws") {
		t.Errorf("test-aws should be removed:\n%s", out)
	}

	t.Log("✅ secrets add/list/show/reveal/remove all work")
}

// ── Kafka SASL proxy test ──

func TestKafkaProxySASL(t *testing.T) {
	ctrName := newContainerName("itest-kp-")
	proxyYAML := `kafka_proxy:
  broker: localhost:9092
  sasl_username: kafkauser
  sasl_password: kafka-secret-password
  tls: false
`
	secretsYAML := `env:
  KAFKA_SASL_USERNAME: placeholder-user
  KAFKA_SASL_PASSWORD: placeholder-pass
`
	setupContainer(t, ctrName, proxyYAML, secretsYAML, false)

	// Start Kafka SASL on host
	kafkaCtr := newContainerName("kafka-sasl-host-")
	t.Cleanup(func() { exec.Command("podman", "rm", "-f", kafkaCtr).Run() })

	kafkaCmd := `cat > /tmp/s.properties << 'P'
node.id=1
process.roles=broker,controller
listeners=CLIENT://:9092,CONTROLLER://:9093
advertised.listeners=CLIENT://127.0.0.1:18082
controller.listener.names=CONTROLLER
controller.quorum.voters=1@localhost:9093
listener.security.protocol.map=CONTROLLER:PLAINTEXT,CLIENT:SASL_PLAINTEXT
sasl.enabled.mechanisms=PLAIN
sasl.mechanism.inter.broker.protocol=PLAIN
inter.broker.listener.name=CLIENT
listener.name.client.plain.sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="admin" password="admin-secret" user_kafkauser="kafka-secret-password";
offsets.topic.replication.factor=1
group.initial.rebalance.delay.ms=0
log.dirs=/tmp/kafka-logs
P
/opt/kafka/bin/kafka-storage.sh format -t MkU3OEVBNTcwNTJENDM2Qk -c /tmp/s.properties --ignore-formatted 2>/dev/null
exec /opt/kafka/bin/kafka-server-start.sh /tmp/s.properties`

	exec.Command("podman", "run", "-d", "--name", kafkaCtr,
		"-p", "9092:9092",
		"--entrypoint", "bash",
		"docker.io/apache/kafka:3.8.0",
		"-c", kafkaCmd,
	).Run()

	ready := false
	for i := 0; i < 30; i++ {
		logs, _ := exec.Command("podman", "logs", kafkaCtr).CombinedOutput()
		if strings.Contains(string(logs), "started") {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		t.Skip("Kafka SASL broker did not start")
	}

	msg := fmt.Sprintf("sasl-proxy-hello-%d", time.Now().UnixNano())

	// Wait for kafka proxy bridge
	waitForBridge(t, ctrName, 18082, 20*time.Second)

	// Produce
	execIn(t, ctrName, fmt.Sprintf(`echo '%s' | kcat -b 127.0.0.1:18082 \
-X security.protocol=SASL_PLAINTEXT -X sasl.mechanism=PLAIN \
-X sasl.username=$KAFKA_SASL_USERNAME -X sasl.password=$KAFKA_SASL_PASSWORD \
-t sasl-test -P -e 2>&1`, msg))

	// Consume
	consumed := strings.TrimSpace(execIn(t, ctrName, fmt.Sprintf(`kcat -b 127.0.0.1:18082 \
-X security.protocol=SASL_PLAINTEXT -X sasl.mechanism=PLAIN \
-X sasl.username=$KAFKA_SASL_USERNAME -X sasl.password=$KAFKA_SASL_PASSWORD \
-t sasl-test -C -e -o beginning -q 2>/dev/null`)))

	if strings.Contains(consumed, msg) {
		t.Logf("✅ Kafka SASL proxy: %q → %q", msg, consumed)
	} else {
		t.Errorf("message mismatch: sent=%q received=%q", msg, consumed)
	}
}

// Ensure unused imports are referenced
var _ = container.DefaultName
var _ = secrets.BuiltinTypes
var _ = context.Background
