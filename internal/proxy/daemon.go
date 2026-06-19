package proxy

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
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

func StartProxyDaemon(name, profile string) (int, error) {
	path := config.ResolveProxyConfigPath(profile)
	cfg := config.LoadProxyConfigFrom(path)
	if cfg == nil {
		return 0, nil
	}

	ca, caKey, err := GenerateOrLoadCA()
	if err != nil {
		return 0, fmt.Errorf("generate CA: %w", err)
	}

	port, err := FindFreePort()
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}

	configJSON, _ := json.Marshal(cfg)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	caKeyDER, _ := x509.MarshalECPrivateKey(caKey)
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	cmd := exec.Command(os.Args[0], "_proxy", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(),
		"AGENT_VM_PROXY_CONFIG="+string(configJSON),
		"AGENT_VM_PROXY_CA_CERT="+string(caCertPEM),
		"AGENT_VM_PROXY_CA_KEY="+string(caKeyPEM),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, _ := os.Create(config.ProxyLogPath(name))
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start proxy daemon: %w", err)
	}

	_ = os.MkdirAll(config.StateDir(), 0o755)
	_ = os.WriteFile(config.ProxyPidPath(name), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()

	for i := 0; i < 20; i++ {
		if ProxyReady(port) {
			fmt.Printf("[agent-vm] MITM proxy started on port %d\n", port)
			return port, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return port, nil
}

func StopProxyDaemon(name string) {
	data, err := os.ReadFile(config.ProxyPidPath(name))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	_ = os.Remove(config.ProxyPidPath(name))
}

func FindFreePort() (int, error) {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func ProxyReady(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
