package vmctl

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

func jsonError(c *echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]string{"error": msg})
}

func jsonSuccess(c *echo.Context, data any) error {
	return c.JSON(http.StatusOK, data)
}

func handleStatus(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		cfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		backend := NewBackend(cfg)
		status, err := backend.Status(cfg)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		var metrics *GuestMetrics
		if status.Running && (cfg.Backend == "" || cfg.Backend == "utm") {
			sample, err := SampleGuestMetrics(cfg)
			if err == nil {
				calculated := CalculateGuestMetrics(sample, nil)
				metrics = &calculated
			}
		}
		return jsonSuccess(c, map[string]any{
			"status":  status,
			"metrics": metrics,
			"config": map[string]any{
				"backend":       cfg.Backend,
				"shell":         cfg.DefaultShell,
				"editor":        cfg.DefaultEditor,
				"windowManager": cfg.WindowManager,
				"memoryMiB":     cfg.MemoryMiB,
				"diskSize":      cfg.DiskSize,
				"staticIP":      cfg.StaticIP,
				"hookScripts":   cfg.BootstrapHookScripts,
				"userName":      cfg.GitUserName,
				"userEmail":     cfg.GitUserEmail,
				"environment":   cfg.Environment,
				"image":         cfg.Image,
				"ports":         cfg.PortMappings,
				"volumes":       cfg.Volumes,
			},
		})
	}
}

func handleBootstrap(cfg Config) echo.HandlerFunc {
	type bootstrapReq struct {
		Shell         string         `json:"shell"`
		Editor        string         `json:"editor"`
		WindowManager string         `json:"windowManager"`
		Backend       string         `json:"backend"`
		MemoryMiB     int            `json:"memoryMiB"`
		DiskSize      string         `json:"diskSize"`
		StaticIP      string         `json:"staticIP"`
		HookScripts   []string       `json:"hookScripts"`
		UserName      string         `json:"userName"`
		UserEmail     string         `json:"userEmail"`
		Environment   string         `json:"environment"`
		Ports         []PortMapping  `json:"ports"`
		Volumes       []VolumeMount  `json:"volumes"`
	}
	return func(c *echo.Context) error {
		var req bootstrapReq
		if err := c.Bind(&req); err != nil {
			return jsonError(c, http.StatusBadRequest, "invalid request")
		}
		newCfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if req.Backend != "" {
			newCfg.Backend = req.Backend
		}
		if req.Shell != "" {
			newCfg.DefaultShell = req.Shell
		}
		if req.Editor != "" {
			newCfg.DefaultEditor = req.Editor
		}
		if req.WindowManager != "" {
			newCfg.WindowManager = req.WindowManager
		}
		if req.MemoryMiB > 0 {
			newCfg.MemoryMiB = req.MemoryMiB
		}
		if req.DiskSize != "" {
			newCfg.DiskSize = req.DiskSize
		}
		if req.StaticIP != "" {
			newCfg.StaticIP = req.StaticIP
		}
		if req.Environment != "" {
			newCfg.Environment = req.Environment
		}
		if len(req.Ports) > 0 {
			newCfg.PortMappings = req.Ports
		}
		if len(req.Volumes) > 0 {
			newCfg.Volumes = req.Volumes
		}
		if len(req.HookScripts) > 0 {
			var parts []string
			for _, path := range req.HookScripts {
				content, err := os.ReadFile(path)
				if err != nil {
					return jsonError(c, http.StatusBadRequest, fmt.Sprintf("failed to read hook script %s: %v", path, err))
				}
				parts = append(parts, string(content))
			}
			newCfg.BootstrapHookScripts = req.HookScripts
			if newCfg.BootstrapExtraCommands != "" {
				newCfg.BootstrapExtraCommands += "\n"
			}
			newCfg.BootstrapExtraCommands += strings.Join(parts, "\n")
		}
		if req.UserName != "" {
			newCfg.GitUserName = req.UserName
		}
		if req.UserEmail != "" {
			newCfg.GitUserEmail = req.UserEmail
		}
		if err := SaveConfig(newCfg); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		go func() {
			backend := NewBackend(newCfg)
			if err := backend.BootstrapSetup(newCfg); err != nil {
				addProgress("bootstrap failed: %v", err)
				fmt.Printf("bootstrap error: %v\n", err)
			} else {
				addProgress("bootstrap completed")
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "bootstrap started"})
	}
}

func handleStart(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		go func() {
			if err := NewBackend(cfg).Start(cfg); err != nil {
				addProgress("start failed: %v", err)
				fmt.Printf("start error: %v\n", err)
			} else {
				addProgress("started")
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "start initiated"})
	}
}

func handleStop(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		go func() {
			if err := NewBackend(cfg).Stop(cfg); err != nil {
				addProgress("stop failed: %v", err)
				fmt.Printf("stop error: %v\n", err)
			} else {
				addProgress("stopped")
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "stop initiated"})
	}
}

func handleDestroy(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		go func() {
			if err := NewBackend(cfg).Destroy(cfg); err != nil {
				addProgress("destroy failed: %v", err)
				fmt.Printf("destroy error: %v\n", err)
			} else {
				addProgress("destroyed")
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "destroy initiated"})
	}
}

func handleUpgradeKernel(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		status, err := InspectVM(cfg)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if !status.Running {
			return jsonError(c, http.StatusServiceUnavailable, "VM is not running")
		}
		go func() {
			version, err := UpgradeKernel(cfg)
			if err != nil {
				addProgress("kernel upgrade failed: %v", err)
				fmt.Printf("kernel upgrade error: %v\n", err)
			} else {
				addProgress("kernel upgraded to %s", version)
				fmt.Printf("[agent-vm] kernel upgraded to %s\n", version)
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "kernel upgrade started"})
	}
}

func handleListTunnels(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		cfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		tc, err := LoadTunnelConfig(tunnelConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		type tunnelWithStatus struct {
			Tunnel
			Running bool `json:"running"`
		}
		var result []tunnelWithStatus
		for _, t := range tc.Tunnels {
			result = append(result, tunnelWithStatus{
				Tunnel:  t,
				Running: IsTunnelRunning(cfg, t),
			})
		}
		return jsonSuccess(c, map[string]any{
			"version": tc.Version,
			"tunnels": result,
		})
	}
}

func handleAddTunnel(cfg Config) echo.HandlerFunc {
	type tunnelReq struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		LocalPort  int    `json:"localPort"`
		RemotePort int    `json:"remotePort"`
		RemoteHost string `json:"remoteHost"`
		AutoStart  bool   `json:"autoStart"`
	}
	return func(c *echo.Context) error {
		var req tunnelReq
		if err := c.Bind(&req); err != nil {
			return jsonError(c, http.StatusBadRequest, "invalid request")
		}
		var tunnelType TunnelType
		if req.Type == "local" {
			tunnelType = TunnelTypeLocal
		} else {
			tunnelType = TunnelTypeRemote
		}
		if req.RemoteHost == "" {
			req.RemoteHost = "localhost"
		}
		tunnel := Tunnel{
			ID:         req.Name,
			Name:       req.Name,
			Type:       tunnelType,
			LocalPort:  req.LocalPort,
			RemoteHost: req.RemoteHost,
			RemotePort: req.RemotePort,
			Enabled:    true,
			AutoStart:  req.AutoStart,
			CreatedAt:  time.Now().UTC(),
		}
		tc, err := LoadTunnelConfig(tunnelConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if err := tc.AddTunnel(tunnel); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if err := SaveTunnelConfig(tunnelConfigPath(cfg), tc); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, tunnel)
	}
}

func handleRemoveTunnel(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		tc, err := LoadTunnelConfig(tunnelConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		tunnel, ok := tc.GetTunnel(id)
		if !ok {
			return jsonError(c, http.StatusNotFound, "tunnel not found")
		}
		if IsTunnelRunning(cfg, tunnel) {
			_ = StopTunnel(cfg, tunnel)
		}
		if !tc.RemoveTunnel(id) {
			return jsonError(c, http.StatusNotFound, "tunnel not found")
		}
		if err := SaveTunnelConfig(tunnelConfigPath(cfg), tc); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, map[string]string{"message": "tunnel removed"})
	}
}

func handleStartTunnel(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		tc, err := LoadTunnelConfig(tunnelConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		tunnel, ok := tc.GetTunnel(id)
		if !ok {
			return jsonError(c, http.StatusNotFound, "tunnel not found")
		}
		status, err := InspectVM(cfg)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if !status.Running {
			return jsonError(c, http.StatusServiceUnavailable, "VM is not running")
		}
		if err := StartTunnel(cfg, tunnel); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, map[string]string{"message": "tunnel started"})
	}
}

func handleStopTunnel(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		tc, err := LoadTunnelConfig(tunnelConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		tunnel, ok := tc.GetTunnel(id)
		if !ok {
			return jsonError(c, http.StatusNotFound, "tunnel not found")
		}
		if err := StopTunnel(cfg, tunnel); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, map[string]string{"message": "tunnel stopped"})
	}
}

func handleProgress() echo.HandlerFunc {
	return func(c *echo.Context) error {
		sinceStr := c.QueryParam("since")
		var since time.Time
		if sinceStr != "" {
			ms, err := strconv.ParseInt(sinceStr, 10, 64)
			if err == nil {
				since = time.UnixMilli(ms)
			}
		}
		entries := getProgressSince(since)
		return jsonSuccess(c, map[string]any{"entries": entries})
	}
}

func handleListSync(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		cfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		sc, err := LoadSyncConfig(syncConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, sc)
	}
}

func handleAddSync(cfg Config) echo.HandlerFunc {
	type syncReq struct {
		Mode                string   `json:"mode"`
		HostPath            string   `json:"hostPath"`
		VMPath              string   `json:"vmPath"`
		BareRepoPath        string   `json:"bareRepoPath"`
		Direction           string   `json:"direction"`
		Exclude             []string `json:"exclude"`
		BackupRetentionDays int      `json:"backupRetentionDays"`
	}
	return func(c *echo.Context) error {
		var req syncReq
		if err := c.Bind(&req); err != nil {
			return jsonError(c, http.StatusBadRequest, "invalid request")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		pair := SyncPair{
			ID:        filepath.Base(req.HostPath),
			HostPath:  req.HostPath,
			VMPath:    req.VMPath,
			CreatedAt: time.Now().UTC(),
		}
		mode := strings.ToLower(req.Mode)
		if mode == "git" {
			pair.Mode = SyncModeGit
			if !IsGitRepo(req.HostPath) {
				return jsonError(c, http.StatusBadRequest, "host directory is not a git repository")
			}
			if req.BareRepoPath != "" {
				pair.BareRepoPath = req.BareRepoPath
			} else {
				pair.BareRepoPath = defaultBareRepoPath(cfg, pair)
			}
		} else {
			pair.Mode = SyncModeCopy
			pair.Direction = SyncDirection(req.Direction)
			pair.Exclude = req.Exclude
			pair.BackupRetentionDays = req.BackupRetentionDays
		}
		sc, err := LoadSyncConfig(syncConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if err := sc.AddPair(pair); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if err := SaveSyncConfig(syncConfigPath(cfg), sc); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if pair.Mode == SyncModeGit {
			if err := GitSetupPair(cfg, pair); err != nil {
				return jsonError(c, http.StatusInternalServerError, err.Error())
			}
		}
		return jsonSuccess(c, pair)
	}
}

func handleRemoveSync(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		sc, err := LoadSyncConfig(syncConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if !sc.RemovePair(id) {
			return jsonError(c, http.StatusNotFound, "sync pair not found")
		}
		if err := SaveSyncConfig(syncConfigPath(cfg), sc); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, map[string]string{"message": "sync pair removed"})
	}
}

func handleRunSync(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		cfg, err := LoadConfig()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		sc, err := LoadSyncConfig(syncConfigPath(cfg))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		pair, ok := sc.GetPair(id)
		if !ok {
			return jsonError(c, http.StatusNotFound, "sync pair not found")
		}
		if pair.Mode == SyncModeGit {
			return jsonSuccess(c, map[string]string{"message": "Use 'git push vm' and 'git pull vm' in your host repo"})
		}
		go func() {
			var err error
			switch pair.Direction {
			case SyncDirectionHostToVM:
				err = CopySyncHostToVM(cfg, pair, syncBackupsDir(cfg))
			case SyncDirectionVMToHost:
				err = CopySyncVMToHost(cfg, pair, syncBackupsDir(cfg))
			case SyncDirectionBidirectional:
				err = CopySyncBidirectional(cfg, pair, syncBackupsDir(cfg))
			}
			if err != nil {
				fmt.Printf("sync error: %v\n", err)
			}
		}()
		return jsonSuccess(c, map[string]string{"message": "sync started"})
	}
}

func handleSyncHistory(cfg Config) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("id")
		backups, err := listBackups(syncBackupsDir(cfg), id)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return jsonSuccess(c, backups)
	}
}
