package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
	"github.com/vrealzhou/agent-vm/internal/credential"
	"github.com/vrealzhou/agent-vm/internal/network"
	"github.com/vrealzhou/agent-vm/internal/proxy"
)

const (
	DefaultName     = "dev"
	kataImageName   = "kata-dev"
	memoryLimit     = "6g"
	workspaceMount  = "/home/vm/workspace"
	opencodeWebPort = 4096
	ttydPort        = 8082
	opencodeBin     = "/home/vm/.opencode/bin/opencode"
	ttydBin         = "/home/linuxbrew/.linuxbrew/bin/ttyd"
)

// Dockerfile holds the embedded kata Dockerfile content. The root main package
// (which owns the //go:embed directive, since embed paths cannot reach above a
// package directory) sets this before Build is invoked.
var Dockerfile string

func ResolveWorkdir(workspace string) (string, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absWorkspace, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return workspaceMount, nil
	}
	return filepath.Join(workspaceMount, rel), nil
}

// PortReady checks whether a port is listening inside the container by
// probing via bash /dev/tcp — no host port required.
func PortReady(name string, port int) bool {
	cmd := exec.Command("container", "exec", name, "bash", "-lc",
		fmt.Sprintf("echo > /dev/tcp/127.0.0.1/%d 2>/dev/null", port))
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// EnsureService starts a background process inside the container if the
// given port is not yet listening.
func EnsureService(name string, port int, startCmd, label string) error {
	if PortReady(name, port) {
		return nil
	}
	if !IsRunning(name) {
		return fmt.Errorf("container %q is not running", name)
	}
	fmt.Printf("[agent-vm] starting %s in container %q ...\n", label, name)
	cmd := exec.Command("container", "exec", "-u", "vm", name, "bash", "-lc",
		fmt.Sprintf("nohup %s > /tmp/%s.log 2>&1 &", startCmd, label))
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start %s: %w", label, err)
	}
	for i := 0; i < 30; i++ {
		if PortReady(name, port) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	log := readContainerFile(name, "/tmp/"+label+".log")
	if strings.TrimSpace(log) == "" {
		log = "(no output captured — the process likely exited before writing anything; check that the binary exists and is executable)"
	}
	return fmt.Errorf("%s did not become ready in container %q — service log:\n%s", label, name, log)
}

// readContainerFile returns the contents of a file inside the container.
func readContainerFile(name, path string) string {
	out, err := exec.Command("container", "exec", name, "cat", path).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func ensureOpencodeWeb(name string) error {
	// BROWSER=/bin/true prevents opencode web from trying to launch a
	// browser inside the headless container (the `open` npm package honors
	// BROWSER as the browser command — /bin/true is a no-op).
	return EnsureService(name, opencodeWebPort,
		fmt.Sprintf("env BROWSER=/bin/true %s web --port %d --hostname 0.0.0.0", opencodeBin, opencodeWebPort),
		"opencode-web")
}

func ensureTTYD(name string) error {
	return EnsureService(name, ttydPort,
		fmt.Sprintf("%s --port %d -W -t fontFamily='Maple Mono NF CN' -t fontSize=14 zsh", ttydBin, ttydPort),
		"ttyd")
}

func Build(goVersion string) error {
	if err := checkContainerCLI(); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "agent-vm-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(Dockerfile), 0o644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}

	args := []string{"build", "-t", kataImageName, tmpDir}
	if goVersion != "" {
		args = append(args, "--build-arg", "GO_VERSION="+goVersion)
	}
	fmt.Printf("[agent-vm] building image %s ...\n", kataImageName)
	return runCommand("container", args...)
}

func IsRunning(name string) bool {
	cmd := exec.Command("container", "exec", name, "true")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func Start(name, cpus, workspace string, useProxy bool, extraPublish []string, profile string) error {
	if err := checkContainerCLI(); err != nil {
		return err
	}

	if IsRunning(name) {
		fmt.Printf("[agent-vm] container %q is already running\n", name)
		return nil
	}

	_ = exec.Command("container", "delete", "-f", name).Run()

	if workspace == "" {
		workspace = config.LoadWorkspace(name)
	}
	if workspace == "" {
		workspace = "."
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	config.SaveWorkspace(name, absWorkspace)
	workdir, err := ResolveWorkdir(workspace)
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}

	// Start credential proxy if configured
	proxyPort := 0
	if useProxy {
		proxyPort, err = proxy.StartProxyDaemon(name, profile)
		if err != nil {
			fmt.Printf("[agent-vm] warning: proxy not started: %v\n", err)
		}
	}

	// Start credential forwarding server if configured
	credPort, err := credential.StartCredentialDaemon(name)
	if err != nil {
		fmt.Printf("[agent-vm] warning: credential server not started: %v\n", err)
	}

	// Start Kafka SASL proxy if configured
	kafkaCfg := proxy.LoadKafkaProxyConfig()
	kafkaPort, err := proxy.StartKafkaProxyDaemon(name, kafkaCfg)
	if err != nil {
		fmt.Printf("[agent-vm] warning: kafka proxy not started: %v\n", err)
	}

	args := []string{
		"run", "-d",
		"--name", name,
		"-c", cpus,
		"-m", memoryLimit,
		"--virtualization",
		"-v", absWorkspace + ":" + workspaceMount,
		"--workdir", workdir,
	}

	// Network config (from network.json + CLI overrides)
	netCfg := config.LoadNetworkConfig()
	args = append(args, network.NetworkConfigToRunArgs(netCfg, extraPublish)...)

	if proxyPort > 0 {
		args = append(args, "-e", fmt.Sprintf("PROXY_PORT=%d", proxyPort))
		args = append(args, "-v", config.CACertPath()+":/tmp/proxy-ca.crt:ro")
	}
	args = append(args, kataImageName)

	// Combine init scripts: proxy + credential + kafka + sleep
	var scriptParts []string
	if proxyPort > 0 {
		scriptParts = append(scriptParts, strings.TrimSpace(proxy.ProxyInitScript(proxyPort)))
	}
	if credPort > 0 {
		scriptParts = append(scriptParts, strings.TrimSpace(credential.CredentialInitScript(credPort)))
	}
	if kafkaPort > 0 {
		scriptParts = append(scriptParts, strings.TrimSpace(proxy.KafkaProxyInitScript(kafkaPort)))
	}
	if len(scriptParts) > 0 {
		args = append(args, "bash", "-c", strings.Join(scriptParts, "\n")+"\nsleep infinity")
	} else {
		args = append(args, "sleep", "infinity")
	}

	fmt.Printf("[agent-vm] starting container %q (workspace: %s)\n", name, absWorkspace)
	if err := runCommand("container", args...); err != nil {
		proxy.StopProxyDaemon(name)
		credential.StopCredentialDaemon(name)
		proxy.StopKafkaProxyDaemon(name)
		return err
	}
	fmt.Printf("[agent-vm] container %q started — attach with: agent-vm exec %s\n", name, name)
	return nil
}

func Exec(name, workspace string) error {
	if err := checkContainerCLI(); err != nil {
		return err
	}
	if workspace == "" {
		workspace = config.LoadWorkspace(name)
	}
	if workspace == "" {
		workspace = "."
	}
	workdir, err := ResolveWorkdir(workspace)
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}
	return runWithSignals(exec.Command(
		"container", "exec", "-it", "-u", "vm",
		"--workdir", workdir,
		name, "/usr/bin/zsh", "-l",
	))
}

func Stop(name string) error {
	if err := checkContainerCLI(); err != nil {
		return err
	}
	proxy.StopProxyDaemon(name)
	credential.StopCredentialDaemon(name)
	proxy.StopKafkaProxyDaemon(name)
	return runCommand("container", "stop", name)
}

func Status() error {
	if err := checkContainerCLI(); err != nil {
		return err
	}
	names := config.ListManagedContainers()
	if len(names) == 0 {
		fmt.Println("No agent-vm managed containers.")
		return nil
	}
	fmt.Printf("%-20s  %-10s  %-10s  %-10s  %s\n", "NAME", "STATE", "OPENCODE", "TERMINAL", "WORKSPACE")
	for _, name := range names {
		running := IsRunning(name)
		ws := config.LoadWorkspace(name)
		webState := "-"
		termState := "-"
		if running {
			if PortReady(name, opencodeWebPort) {
				webState = "running"
			} else {
				webState = "idle"
			}
			if PortReady(name, ttydPort) {
				termState = "running"
			} else {
				termState = "idle"
			}
		}
		stateStr := "stopped"
		if running {
			stateStr = "running"
		}
		fmt.Printf("%-20s  %-10s  %-10s  %-10s  %s\n", name, stateStr, webState, termState, ws)
	}
	return nil
}

func Destroy(name string) error {
	if err := checkContainerCLI(); err != nil {
		return err
	}
	proxy.StopProxyDaemon(name)
	credential.StopCredentialDaemon(name)
	proxy.StopKafkaProxyDaemon(name)
	_ = exec.Command("container", "delete", "-f", name).Run()
	config.RemoveWorkspaceState(name)
	return nil
}
