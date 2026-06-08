package vmctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type VMStatus struct {
	Name          string
	State         string
	Running       bool
	PID           int
	DiskPath      string
	StaticIP      string
	SSHTarget     string
	BootstrapDone bool
	KernelVersion string
}

type GuestMetricsSample struct {
	CPUTotalTicks uint64
	CPUIdleTicks  uint64
	MemTotalMiB   int
	MemUsedMiB    int
	CollectedAt   time.Time
}

type GuestMetrics struct {
	Available      bool
	SSHReady       bool
	CPUPercent     float64
	HasCPUPercent  bool
	MemTotalMiB    int
	MemUsedMiB     int
	MemUsedPercent float64
}

func InspectVM(cfg Config) (VMStatus, error) {
	status := VMStatus{
		Name:          cfg.Name,
		State:         "stopped",
		DiskPath:      cfg.DiskPath,
		StaticIP:      cfg.StaticIP,
		SSHTarget:     fmt.Sprintf("%s@%s", cfg.SSHUser, cfg.StaticIP),
		BootstrapDone: fileExists(cfg.BootstrapMarker),
	}
	if fileExists(cfg.KernelPath) {
		status.KernelVersion = filepath.Base(cfg.KernelPath)
	}

	running, err := utmVMIsRunning(cfg.Name)
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

func Destroy(cfg Config) error {
	running, err := utmVMIsRunning(cfg.Name)
	if err != nil {
		return err
	}
	if running {
		if err := Stop(cfg); err != nil {
			return err
		}
	}

	if fileExists(cfg.UTMBundlePath) {
		if err := os.RemoveAll(cfg.UTMBundlePath); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		return err
	}
	return nil
}

func utmVMIsRunning(name string) (bool, error) {
	out, err := exec.Command("utmctl", "status", name).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "running"), nil
}

func SampleGuestMetrics(cfg Config) (GuestMetricsSample, error) {
	cmd := exec.Command("ssh", append(sshProbeArgsForUser(cfg, cfg.SSHUser), guestMetricsCommand())...)
	output, err := cmd.Output()
	if err != nil {
		return GuestMetricsSample{}, err
	}
	sample, err := parseGuestMetricsSample(string(output))
	if err != nil {
		return GuestMetricsSample{}, err
	}
	sample.CollectedAt = time.Now()
	return sample, nil
}

func guestMetricsCommand() string {
	return "sh -lc " + shellQuote("awk '/^cpu / {printf \"cpu\"; for (i=2; i<=NF; i++) printf \" %s\", $i; printf \"\\n\"; exit}' /proc/stat; awk '/^MemTotal:/ {print \"mem_total \" $2} /^MemAvailable:/ {print \"mem_available \" $2}' /proc/meminfo")
}

func parseGuestMetricsSample(output string) (GuestMetricsSample, error) {
	var (
		cpuFields       []string
		memTotalKiB     int
		memAvailableKiB int
		haveTotal       bool
		haveAv          bool
	)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "cpu":
			if len(fields) < 5 {
				return GuestMetricsSample{}, fmt.Errorf("unexpected cpu metrics: %q", line)
			}
			cpuFields = fields[1:]
		case "mem_total":
			value, err := strconv.Atoi(fieldAt(fields, 1))
			if err != nil {
				return GuestMetricsSample{}, fmt.Errorf("unexpected mem_total metrics: %q", line)
			}
			memTotalKiB = value
			haveTotal = true
		case "mem_available":
			value, err := strconv.Atoi(fieldAt(fields, 1))
			if err != nil {
				return GuestMetricsSample{}, fmt.Errorf("unexpected mem_available metrics: %q", line)
			}
			memAvailableKiB = value
			haveAv = true
		}
	}

	if len(cpuFields) == 0 || !haveTotal || !haveAv {
		return GuestMetricsSample{}, fmt.Errorf("incomplete guest metrics output")
	}

	var total, idle uint64
	for i, raw := range cpuFields {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return GuestMetricsSample{}, fmt.Errorf("unexpected cpu tick value %q", raw)
		}
		total += value
		if i == 3 || i == 4 {
			idle += value
		}
	}

	memTotalMiB := memTotalKiB / 1024
	memUsedMiB := (memTotalKiB - memAvailableKiB) / 1024
	if memUsedMiB < 0 {
		memUsedMiB = 0
	}

	return GuestMetricsSample{
		CPUTotalTicks: total,
		CPUIdleTicks:  idle,
		MemTotalMiB:   memTotalMiB,
		MemUsedMiB:    memUsedMiB,
	}, nil
}

func CalculateGuestMetrics(sample GuestMetricsSample, previous *GuestMetricsSample) GuestMetrics {
	metrics := GuestMetrics{
		Available:      true,
		SSHReady:       true,
		MemTotalMiB:    sample.MemTotalMiB,
		MemUsedMiB:     sample.MemUsedMiB,
		MemUsedPercent: percent(sample.MemUsedMiB, sample.MemTotalMiB),
	}

	if previous == nil {
		return metrics
	}

	deltaTotal := sample.CPUTotalTicks - previous.CPUTotalTicks
	deltaIdle := sample.CPUIdleTicks - previous.CPUIdleTicks
	if deltaTotal == 0 || deltaIdle > deltaTotal {
		return metrics
	}

	metrics.CPUPercent = 100 * (float64(deltaTotal-deltaIdle) / float64(deltaTotal))
	metrics.HasCPUPercent = true
	return metrics
}

func sshProbeArgsForUser(cfg Config, user string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=2",
	}
	args = append(args, sshArgsForUser(cfg, user)...)
	return args
}

func fieldAt(fields []string, index int) string {
	if index >= 0 && index < len(fields) {
		return fields[index]
	}
	return ""
}

func percent(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return 100 * (float64(numerator) / float64(denominator))
}
