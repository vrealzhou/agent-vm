package vmctl

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type PodmanBackend struct{}

func (p *PodmanBackend) Start(cfg Config) error {
	if _, err := exec.LookPath("podman"); err != nil {
		return fmt.Errorf("missing required command: podman")
	}

	running, err := p.IsRunning(cfg)
	if err != nil {
		return err
	}
	if running {
		logf("container %s is already running", cfg.ContainerName)
		_, _ = p.Status(cfg)
		return nil
	}

	if cfg.Image == "" {
		return fmt.Errorf("no image specified — set 'image:' or 'environment:' in config, then run 'agent-vm build-image'")
	}

	exists, err := podmanContainerExists(cfg.ContainerName)
	if err != nil {
		return err
	}

	if exists {
		logf("starting existing container %s", cfg.ContainerName)
		addProgress("starting container...")
		if err := exec.Command("podman", "start", cfg.ContainerName).Run(); err != nil {
			return fmt.Errorf("podman start failed: %w", err)
		}
	} else {
		if err := verifyLibkrunDriver(); err != nil {
			return fmt.Errorf("libkrun verification: %w", err)
		}

		imageExists, err := podmanImageExists(cfg.Image)
		if err != nil {
			return err
		}
		if !imageExists {
			return fmt.Errorf("image %s not found — run 'agent-vm build-image --profile %s' first", cfg.Image, cfg.Environment)
		}

		logf("creating container %s from image %s", cfg.ContainerName, cfg.Image)
		addProgress("creating container...")
		args := podmanRunArgs(cfg)
		cmd := exec.Command("podman", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("podman run failed: %w", err)
		}
	}

	if !fileExists(cfg.BootstrapMarker) {
		if err := p.runHooks(cfg); err != nil {
			logf("hook scripts: %v", err)
		}
		if err := os.WriteFile(cfg.BootstrapMarker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
			return err
		}
		addProgress("bootstrap complete")
	}

	return nil
}

func (p *PodmanBackend) Stop(cfg Config) error {
	running, err := p.IsRunning(cfg)
	if err != nil {
		return err
	}
	if !running {
		logf("container %s is not running", cfg.ContainerName)
		return nil
	}

	logf("stopping container %s", cfg.ContainerName)
	if err := exec.Command("podman", "stop", cfg.ContainerName).Run(); err != nil {
		return fmt.Errorf("podman stop failed: %w", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		running, err = p.IsRunning(cfg)
		if err != nil {
			return err
		}
		if !running {
			logf("container stopped")
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("container did not stop in time")
}

func (p *PodmanBackend) Destroy(cfg Config) error {
	running, _ := p.IsRunning(cfg)
	if running {
		if err := p.Stop(cfg); err != nil {
			return err
		}
	}

	exec.Command("podman", "rm", "-f", cfg.ContainerName).Run()
	os.RemoveAll(cfg.StateDir)
	logf("container %s destroyed", cfg.ContainerName)
	return nil
}

func (p *PodmanBackend) IsRunning(cfg Config) (bool, error) {
	out, err := exec.Command("podman", "inspect", "--format", "{{.State.Running}}", cfg.ContainerName).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func (p *PodmanBackend) Status(cfg Config) (VMStatus, error) {
	status := VMStatus{
		Name:          cfg.Name,
		State:         "stopped",
		DiskPath:      cfg.Image,
		StaticIP:      cfg.StaticIP,
		SSHTarget:     fmt.Sprintf("%s@%s", cfg.SSHUser, cfg.StaticIP),
		BootstrapDone: fileExists(cfg.BootstrapMarker),
	}

	running, err := p.IsRunning(cfg)
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

func (p *PodmanBackend) Exec(cfg Config, args ...string) error {
	execArgs := []string{"exec", "-it", "-u", cfg.GuestUser, "--workdir", "/home/" + cfg.GuestUser, cfg.ContainerName}
	execArgs = append(execArgs, args...)
	cmd := exec.Command("podman", execArgs...)
	return runWithSignals(cmd)
}

func (p *PodmanBackend) BootstrapSetup(cfg Config) error {
	if cfg.Image == "" && cfg.Environment != "" {
		profile, err := ResolveProfile(cfg.ConfigDir, cfg.Environment)
		if err != nil {
			return fmt.Errorf("failed to resolve profile: %w", err)
		}
		addProgress("generating Dockerfile from profile %s...", profile.Name)
		if err := WriteBuildContext(cfg, profile); err != nil {
			return err
		}
		cfg.Image = ProfileImageName(profile)

		addProgress("building image %s...", cfg.Image)
		if err := podmanBuildImage(cfg); err != nil {
			return err
		}
	}

	return p.Start(cfg)
}

func (p *PodmanBackend) runHooks(cfg Config) error {
	if cfg.BootstrapExtraCommands == "" {
		return nil
	}
	addProgress("running hook scripts...")
	script := cfg.BootstrapExtraCommands
	execArgs := []string{"exec", "-u", cfg.GuestUser, cfg.ContainerName, "bash", "-c", script}
	cmd := exec.Command("podman", execArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func podmanRunArgs(cfg Config) []string {
	args := []string{
		"run", "-d",
		"--name", cfg.ContainerName,
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "4096",
		"--tmpfs", "/tmp:rw,noexec,nosuid",
		"--tmpfs", "/run:rw,noexec,nosuid",
		"--tmpfs", "/var/tmp:rw,noexec,nosuid",
		"-v", cfg.ContainerName + "_home:/home/vm",
		"-v", cfg.ContainerName + "_brew:/home/linuxbrew/.linuxbrew",
	}

	if cfg.MemoryMiB > 0 {
		args = append(args, "--memory", strconv.Itoa(cfg.MemoryMiB)+"m")
	}

	for _, pm := range cfg.PortMappings {
		args = append(args, "-p", fmt.Sprintf("%d:%d", pm.Host, pm.Guest))
	}

	for _, vol := range cfg.Volumes {
		if vol.HostPath != "" {
			args = append(args, "-v", fmt.Sprintf("%s:%s", vol.HostPath, vol.Mount))
		} else {
			args = append(args, "-v", fmt.Sprintf("%s:%s", vol.Name, vol.Mount))
		}
	}

	args = append(args, cfg.Image)
	return args
}

func podmanContainerExists(name string) (bool, error) {
	err := exec.Command("podman", "inspect", name).Run()
	return err == nil, nil
}

func podmanImageExists(name string) (bool, error) {
	err := exec.Command("podman", "image", "inspect", name).Run()
	return err == nil, nil
}

func verifyLibkrunDriver() error {
	out, err := exec.Command("podman", "machine", "inspect", "--format", "{{.DriverInfo.Name}}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman machine not running: %w", err)
	}
	driver := strings.TrimSpace(string(out))
	if driver != "libkrun" {
		return fmt.Errorf("podman machine driver is %q, expected libkrun. Run: podman machine set --driver libkrun", driver)
	}
	return nil
}

func podmanBuildImage(cfg Config) error {
	cmd := exec.Command("podman", "build", "-t", cfg.Image, cfg.StateDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *PodmanBackend) SampleMetrics(cfg Config) (GuestMetricsSample, error) {
	out, err := exec.Command("podman", "stats", "--no-stream", "--format", "json", cfg.ContainerName).CombinedOutput()
	if err != nil {
		return GuestMetricsSample{}, err
	}

	var stats []struct {
		CPUPerc  string `json:"CPU"`
		MemUsage string `json:"MemUsage"`
	}
	if err := json.Unmarshal(out, &stats); err != nil || len(stats) == 0 {
		return GuestMetricsSample{}, fmt.Errorf("failed to parse podman stats: %w", err)
	}

	return GuestMetricsSample{}, nil
}
