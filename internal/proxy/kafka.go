package proxy

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// ── Daemon lifecycle ──

func StartKafkaProxyDaemon(name string, cfg *config.KafkaProxyConfig) (int, error) {
	if cfg == nil || cfg.Broker == "" {
		return 0, nil
	}
	port, err := FindFreePort()
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}

	cfgJSON, _ := json.Marshal(cfg)
	cmd := exec.Command(os.Args[0], "_kafka-proxy", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "AGENT_VM_KAFKA_CONFIG="+string(cfgJSON))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, _ := os.Create(config.KafkaLogPath(name))
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start kafka proxy: %w", err)
	}
	_ = os.WriteFile(config.KafkaPidPath(name), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()

	for i := 0; i < 20; i++ {
		if ProxyReady(port) {
			fmt.Printf("[agent-vm] kafka proxy started on port %d → %s\n", port, cfg.Broker)
			return port, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return port, nil
}

func StopKafkaProxyDaemon(name string) {
	data, err := os.ReadFile(config.KafkaPidPath(name))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	_ = os.Remove(config.KafkaPidPath(name))
}

// LoadKafkaProxyConfig reads the kafka_proxy section from proxy.yaml.
func LoadKafkaProxyConfig() *config.KafkaProxyConfig {
	cfg := config.LoadProxyConfig()
	if cfg == nil || cfg.KafkaProxy == nil {
		return nil
	}
	return cfg.KafkaProxy
}

// KafkaProxyInitScript returns a bash snippet that bridges to the kafka proxy.
func KafkaProxyInitScript(kafkaPort int) string {
	if kafkaPort == 0 {
		return ""
	}
	const script = `# Kafka credential proxy bridge
GW=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route 2>/dev/null)
if [ -n "$GW" ] && [ ${#GW} -eq 8 ]; then
    GW=$(printf "%d.%d.%d.%d" 0x${GW:6:2} 0x${GW:4:2} 0x${GW:2:2} 0x${GW:0:2})
fi
if [ -n "$GW" ]; then
    socat TCP-LISTEN:18082,fork,reuseaddr,bind=127.0.0.1 TCP:$GW:__KAFKAPORT__ >/tmp/kafka-bridge.log 2>&1 &
    printf '\nexport KAFKA_BOOTSTRAP_SERVERS=127.0.0.1:18082\nexport KAFKA_SECURITY_PROTOCOL=SASL_PLAINTEXT\n' >> ~/.dev-tools.sh
    echo "[agent-vm] kafka proxy bridge: 127.0.0.1:18082 -> $GW:__KAFKAPORT__"
fi`
	return strings.ReplaceAll(script, "__KAFKAPORT__", strconv.Itoa(kafkaPort))
}

// ── Kafka proxy server ──

func RunKafkaProxyServer(port int) error {
	var cfg config.KafkaProxyConfig
	if raw := os.Getenv("AGENT_VM_KAFKA_CONFIG"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("parse kafka config: %w", err)
		}
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("kafka proxy listen: %w", err)
	}
	fmt.Printf("[agent-vm] kafka proxy on 0.0.0.0:%d → %s (TLS=%v)\n", port, cfg.Broker, cfg.TLS)

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handleKafkaClient(conn, &cfg)
	}
}

func handleKafkaClient(client net.Conn, cfg *config.KafkaProxyConfig) {
	defer client.Close()

	// Connect to real broker (with TLS if configured)
	var broker net.Conn
	var err error
	if cfg.TLS {
		broker, err = tls.Dial("tcp", cfg.Broker, &tls.Config{InsecureSkipVerify: true})
	} else {
		broker, err = net.Dial("tcp", cfg.Broker)
	}
	if err != nil {
		fmt.Printf("[kafka-proxy] connect broker %s: %v\n", cfg.Broker, err)
		return
	}
	defer broker.Close()
	fmt.Printf("[kafka-proxy] client connected, broker %s ready\n", cfg.Broker)

	done := make(chan struct{}, 2)

	// client → broker: intercept SASL_AUTHENTICATE
	go func() {
		pipeKafkaClientToBroker(client, broker, cfg)
		done <- struct{}{}
	}()

	// broker → client: transparent
	go func() {
		io.Copy(client, broker)
		done <- struct{}{}
	}()

	<-done
}

func pipeKafkaClientToBroker(client io.Reader, broker io.Writer, cfg *config.KafkaProxyConfig) {
	for {
		body, err := readKafkaFrame(client)
		if err != nil {
			fmt.Printf("[kafka-proxy] client read ended: %v\n", err)
			return
		}
		apiKey := getKafkaAPIKey(body)
		hexPreview := len(body)
		if hexPreview > 40 {
			hexPreview = 40
		}
		fmt.Printf("[kafka-proxy] frame: api_key=%d size=%d hex=%x\n", apiKey, len(body), body[:hexPreview])

		if apiKey == 36 {
			body = rewriteSASLPlain(body, cfg.SASLUsername, cfg.SASLPassword)
			fmt.Printf("[kafka-proxy] SASL_AUTHENTICATE: credentials replaced → %s\n", cfg.SASLUsername)
		}

		if err := writeKafkaFrame(broker, body); err != nil {
			fmt.Printf("[kafka-proxy] broker write ended: %v\n", err)
			return
		}
	}
}

// ── Kafka protocol frame helpers ──

// readKafkaFrame reads one complete Kafka frame: [4-byte size][body].
func readKafkaFrame(r io.Reader) ([]byte, error) {
	var sizeBuf [4]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return nil, err
	}
	size := int32(binary.BigEndian.Uint32(sizeBuf[:]))
	if size <= 0 || size > 100*1024*1024 {
		return nil, fmt.Errorf("invalid kafka frame size: %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeKafkaFrame writes one complete Kafka frame.
func writeKafkaFrame(w io.Writer, body []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// getKafkaAPIKey extracts the API key from a Kafka request body.
func getKafkaAPIKey(body []byte) int16 {
	if len(body) < 2 {
		return -1
	}
	return int16(binary.BigEndian.Uint16(body[0:2]))
}

// rewriteSASLPlain replaces username/password in a SASL_AUTHENTICATE request.
//
// SASL_AUTHENTICATE request layout:
//   [2B api_key=19] [2B api_version] [4B correlation_id]
//   [2B client_id_len] [client_id...] (or -1 for null)
//   [4B auth_data_len] [auth_data...]
//
// PLAIN auth_data format: \0username\0password
func rewriteSASLPlain(body []byte, username, password string) []byte {
	pos := 8 // skip api_key(2) + version(2) + correlation_id(4)

	// Skip client_id (nullable string)
	if pos+2 > len(body) {
		return body
	}
	clientIDLen := int16(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if clientIDLen >= 0 {
		pos += int(clientIDLen)
	}

	// Build new auth data: \0username\0password
	authData := make([]byte, 0, 2+len(username)+len(password))
	authData = append(authData, 0)
	authData = append(authData, []byte(username)...)
	authData = append(authData, 0)
	authData = append(authData, []byte(password)...)

	// Reconstruct frame: header + client_id + new auth_data
	header := body[:pos]
	result := make([]byte, pos+4+len(authData))
	copy(result, header)
	binary.BigEndian.PutUint32(result[pos:pos+4], uint32(len(authData)))
	copy(result[pos+4:], authData)
	return result
}
