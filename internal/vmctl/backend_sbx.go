package vmctl

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type SBXBackend struct{}

func (s *SBXBackend) Start(cfg Config) error {
	if _, err := exec.LookPath("sbx"); err != nil {
		return fmt.Errorf("missing required command: sbx (install with: brew install docker/tap/sbx)")
	}

	running, err := s.IsRunning(cfg)
	if err != nil {
		return err
	}
	if running {
		logf("sandbox %s is already running", cfg.ContainerName)
		_, _ = s.Status(cfg)
		return nil
	}

	exists, err := sbxSandboxExists(cfg.ContainerName)
	if err != nil {
		return err
	}

	if !exists {
		logf("creating sandbox %s", cfg.ContainerName)
		addProgress("creating sandbox...")
		workspace := "."
		if len(cfg.Volumes) > 0 && cfg.Volumes[0].HostPath != "" {
			workspace = cfg.Volumes[0].HostPath
		}
		cmd := exec.Command("sbx", "create", "--name", cfg.ContainerName, ".")
		cmd.Dir = workspace
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("sbx create failed: %w", err)
		}
	}

	logf("starting sandbox %s", cfg.ContainerName)
	addProgress("starting sandbox...")
	if err := exec.Command("sbx", "run", cfg.ContainerName).Run(); err != nil {
		return fmt.Errorf("sbx run failed: %w", err)
	}

	if !fileExists(cfg.BootstrapMarker) {
		if err := s.runHooks(cfg); err != nil {
			logf("hook scripts: %v", err)
		}
		if err := os.WriteFile(cfg.BootstrapMarker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
			return err
		}
		addProgress("bootstrap complete")
	}

	for _, pm := range cfg.PortMappings {
		publishArgs := []string{"ports", cfg.ContainerName, "--publish", fmt.Sprintf("%d:%d", pm.Host, pm.Guest)}
		if err := exec.Command("sbx", publishArgs...).Run(); err != nil {
			logf("port publish %d:%d: %v", pm.Host, pm.Guest, err)
		}
	}

	return nil
}

func (s *SBXBackend) Stop(cfg Config) error {
	running, err := s.IsRunning(cfg)
	if err != nil {
		return err
	}
	if !running {
		logf("sandbox %s is not running", cfg.ContainerName)
		return nil
	}

	logf("stopping sandbox %s", cfg.ContainerName)
	if err := exec.Command("sbx", "stop", cfg.ContainerName).Run(); err != nil {
		return fmt.Errorf("sbx stop failed: %w", err)
	}
	logf("sandbox stopped")
	return nil
}

func (s *SBXBackend) Destroy(cfg Config) error {
	running, _ := s.IsRunning(cfg)
	if running {
		_ = s.Stop(cfg)
	}

	exec.Command("sbx", "rm", cfg.ContainerName).Run()
	os.RemoveAll(cfg.StateDir)
	logf("sandbox %s destroyed", cfg.ContainerName)
	return nil
}

func (s *SBXBackend) IsRunning(cfg Config) (bool, error) {
	out, err := exec.Command("sbx", "ls", "--format", "json").CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), cfg.ContainerName) && strings.Contains(string(out), "running"), nil
}

func (s *SBXBackend) Status(cfg Config) (VMStatus, error) {
	status := VMStatus{
		Name:          cfg.Name,
		State:         "stopped",
		DiskPath:      cfg.Image,
		StaticIP:      cfg.StaticIP,
		SSHTarget:     fmt.Sprintf("%s@%s", cfg.SSHUser, cfg.StaticIP),
		BootstrapDone: fileExists(cfg.BootstrapMarker),
	}

	running, err := s.IsRunning(cfg)
	if err != nil {
		return status, err
	}
	if !running {
		return status, nil
	}

	status.Running = true
	status.State = "running"
	return status, nil
}

func (s *SBXBackend) Exec(cfg Config, args ...string) error {
	execArgs := []string{"exec", "-it", cfg.ContainerName}
	execArgs = append(execArgs, args...)
	cmd := exec.Command("sbx", execArgs...)
	return runWithSignals(cmd)
}

func (s *SBXBackend) BootstrapSetup(cfg Config) error {
	return s.Start(cfg)
}

func (s *SBXBackend) runHooks(cfg Config) error {
	if cfg.BootstrapExtraCommands == "" {
		return nil
	}
	addProgress("running hook scripts...")
	script := cfg.BootstrapExtraCommands
	execArgs := []string{"exec", cfg.ContainerName, "bash", "-c", script}
	cmd := exec.Command("sbx", execArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sbxSandboxExists(name string) (bool, error) {
	out, err := exec.Command("sbx", "ls").CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), name), nil
}
