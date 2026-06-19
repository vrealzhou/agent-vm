package credential

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
	"github.com/vrealzhou/agent-vm/internal/proxy"
)

// ── Daemon lifecycle ──

func StartCredentialDaemon(name string) (int, error) {
	cfg := config.LoadSecretsConfig()
	if cfg == nil {
		return 0, nil
	}

	port, err := proxy.FindFreePort()
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}

	configJSON, _ := json.Marshal(cfg)
	cmd := exec.Command(os.Args[0], "_credential", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "AGENT_VM_SECRETS_CONFIG="+string(configJSON))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, _ := os.Create(config.CredLogPath(name))
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start credential daemon: %w", err)
	}
	_ = os.WriteFile(config.CredPidPath(name), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()

	for i := 0; i < 20; i++ {
		if proxy.ProxyReady(port) {
			fmt.Printf("[agent-vm] credential server started on port %d\n", port)
			return port, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return port, nil
}

func StopCredentialDaemon(name string) {
	data, err := os.ReadFile(config.CredPidPath(name))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	_ = os.Remove(config.CredPidPath(name))
}

// ── Credential server ──

type credRequest struct {
	Action    string `json:"action"`
	ServerURL string `json:"server_url,omitempty"`
}

type credResponse struct {
	ServerURL string            `json:"ServerURL,omitempty"`
	Username  string            `json:"Username,omitempty"`
	Secret    string            `json:"Secret,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Error     string            `json:"error,omitempty"`
}

func RunCredentialServer(port int) error {
	var cfg *config.SecretsConfig
	if raw := os.Getenv("AGENT_VM_SECRETS_CONFIG"); raw != "" {
		cfg = &config.SecretsConfig{}
		_ = json.Unmarshal([]byte(raw), cfg)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("credential listen: %w", err)
	}
	fmt.Printf("[agent-vm] credential server on 0.0.0.0:%d\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handleCredConn(conn, cfg)
	}
}

func handleCredConn(conn net.Conn, cfg *config.SecretsConfig) {
	defer conn.Close()
	var req credRequest
	if json.NewDecoder(conn).Decode(&req) != nil {
		return
	}
	switch req.Action {
	case "get":
		var resp credResponse
		resp = lookupCredential(cfg, req.ServerURL)
		json.NewEncoder(conn).Encode(resp)
	case "env":
		var resp credResponse
		if cfg != nil && cfg.Env != nil {
			resp.Env = cfg.Env
		}
		json.NewEncoder(conn).Encode(resp)
	case "env-shell":
		// Return shell-exportable lines directly (no JSON parsing needed)
		if cfg != nil && cfg.Env != nil {
			for k, v := range cfg.Env {
				fmt.Fprintf(conn, "export %s=%s\n", k, shellQuoteCred(v))
			}
		}
	}
}

func shellQuoteCred(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func lookupCredential(cfg *config.SecretsConfig, serverURL string) credResponse {
	if cfg == nil || cfg.Credentials == nil {
		return credResponse{Error: "not found"}
	}
	if entry, ok := cfg.Credentials[serverURL]; ok {
		return credResponse{ServerURL: serverURL, Username: entry.Username, Secret: entry.Secret, Env: entry.Env}
	}
	for pattern, entry := range cfg.Credentials {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(serverURL, pattern[1:]) {
			return credResponse{ServerURL: serverURL, Username: entry.Username, Secret: entry.Secret, Env: entry.Env}
		}
	}
	return credResponse{Error: "not found"}
}

// ── Container-side init script ──

// CredentialInitScript sets up socat bridge, env vars, and helper scripts.
func CredentialInitScript(credPort int) string {
	if credPort == 0 {
		return ""
	}
	const script = `# Credential bridge + helpers
GW=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route 2>/dev/null)
if [ -n "$GW" ] && [ ${#GW} -eq 8 ]; then
    GW=$(printf "%d.%d.%d.%d" 0x${GW:6:2} 0x${GW:4:2} 0x${GW:2:2} 0x${GW:0:2})
fi
if [ -n "$GW" ]; then
    socat TCP-LISTEN:18081,fork,reuseaddr,bind=127.0.0.1 TCP:$GW:__CREDPORT__ >/tmp/cred-bridge.log 2>&1 &

    # Fetch env vars from credential server (shell format, no python needed)
    ENV_OUTPUT=$(printf '{"action":"env-shell"}' | socat - TCP:127.0.0.1:18081 2>/dev/null || true)
    if [ -n "$ENV_OUTPUT" ]; then
        printf '\n# Injected credentials\n' >> ~/.dev-tools.sh
        echo "$ENV_OUTPUT" >> ~/.dev-tools.sh
    fi

    # Install generic credential helper
    mkdir -p ~/.local/bin
    cat > ~/.local/bin/agentvm-cred << 'HELPER'
#!/bin/bash
SOCKET="127.0.0.1:18081"
ACTION="${1:-get}"
URL="${2:-}"
if [ "$ACTION" = "get" ] && [ -z "$URL" ]; then
    while IFS='=' read -r k v; do
        case "$k" in protocol) P="$v";; host) H="$v";; esac
    done
    URL="${P}://${H}"
fi
printf '{"action":"%s","server_url":"%s"}' "$ACTION" "$URL" | socat - TCP:$SOCKET 2>/dev/null
HELPER
    chmod +x ~/.local/bin/agentvm-cred

    # Install git credential helper
    cat > ~/.local/bin/git-credential-agentvm << 'GIT'
#!/bin/bash
INPUT=$(cat)
PROTO=$(echo "$INPUT" | grep "^protocol=" | cut -d= -f2)
HOST=$(echo "$INPUT" | grep "^host=" | cut -d= -f2)
RESULT=$(printf '{"action":"get","server_url":"%s://%s"}' "$PROTO" "$HOST" | socat - TCP:127.0.0.1:18081 2>/dev/null)
USER=$(echo "$RESULT" | grep -o '"Username":"[^"]*"' | head -1 | cut -d'"' -f4)
PASS=$(echo "$RESULT" | grep -o '"Secret":"[^"]*"' | head -1 | cut -d'"' -f4)
[ -n "$USER" ] && echo "username=$USER"
[ -n "$PASS" ] && echo "password=$PASS"
GIT
    chmod +x ~/.local/bin/git-credential-agentvm
    git config --global credential.helper agentvm 2>/dev/null || true

    echo "[agent-vm] credential bridge + helpers ready"
fi`
	return strings.ReplaceAll(script, "__CREDPORT__", strconv.Itoa(credPort))
}
