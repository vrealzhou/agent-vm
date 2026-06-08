package vmctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type VolumeMount struct {
	Name     string `yaml:"name"`
	HostPath string `yaml:"host_path"`
	Mount    string `yaml:"mount"`
}

type PortMapping struct {
	Host  int `yaml:"host"`
	Guest int `yaml:"guest"`
}

type Config struct {
	RepoRoot               string
	Name                   string
	Backend                string
	ContainerName          string
	StateDir               string
	UTMBundlePath          string
	DiskPath               string
	KernelPath             string
	InitrdPath             string
	BootstrapMarker        string
	CPUs                   int
	MemoryMiB              int
	DiskSize               string
	MAC                    string
	StaticIP               string
	Gateway                string
	CIDR                   int
	DNSServers             string
	SSHUser                string
	GuestUser              string
	GuestPassword          string
	RootPassword           string
	SSHPublicKey           string
	SSHPrivateKey          string
	SSHKnownHostsFile      string
	Timezone               string
	DefaultShell           string
	DefaultEditor          string
	WindowManager          string
	GitUserName            string
	GitUserEmail           string
	SetDefaultShell        bool
	BootstrapExtraCommands string
	BootstrapHookScripts   []string
	Environment            string
	Image                  string
	VoidRepository         string
	ImageDir               string
	BaseImage              string
	BaseImageURL           string
	GUI                    bool
	Width                  int
	Height                 int
	ConfigDir              string
	SyncPairs              []SyncPair
	Tunnels                []Tunnel
	Volumes                []VolumeMount
	PortMappings           []PortMapping
}

func LoadConfig() (Config, error) {
	configDir, err := determineConfigDir()
	if err != nil {
		return Config{}, err
	}
	yamlPath := filepath.Join(configDir, "vmctl.yaml")

	vcfg, err := loadVMConfigFile(yamlPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to load config: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}

	sshPublicKey := vcfg.User.SSHPublicKey
	if sshPublicKey == "" {
		sshPublicKey = filepath.Join(homeDir, ".ssh", "id_ed25519.pub")
	}

	dnsServersStr := strings.Join(vcfg.Network.DNSServers, " ")

	extraCommands := ""

	for _, path := range vcfg.Bootstrap.HookScripts {
		content, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("failed to read hook script %q: %w", path, err)
		}
		if extraCommands != "" {
			extraCommands += "\n"
		}
		extraCommands += string(content)
	}

	stateDir := filepath.Join(configDir, vcfg.VM.Name)
	imageDir := filepath.Join(configDir, "images")
	utmBundlePath := stateDir + ".utm"
	containerName := "agent-vm-" + vcfg.VM.Name

	cfg := Config{
		ConfigDir:              configDir,
		Name:                   vcfg.VM.Name,
		Backend:                vcfg.Backend,
		ContainerName:          containerName,
		StateDir:               stateDir,
		BootstrapMarker:        filepath.Join(stateDir, "bootstrap.done"),
		CPUs:                   vcfg.VM.CPUs,
		MemoryMiB:              vcfg.VM.MemoryMiB,
		DiskSize:               vcfg.VM.DiskSize,
		SSHUser:                vcfg.User.Name,
		GuestUser:              vcfg.User.Name,
		GuestPassword:          vcfg.User.Password,
		RootPassword:           vcfg.User.RootPassword,
		SSHPublicKey:           sshPublicKey,
		SSHPrivateKey:          strings.TrimSuffix(sshPublicKey, ".pub"),
		SSHKnownHostsFile:      "",
		Timezone:               vcfg.Guest.Timezone,
		DefaultShell:           vcfg.Guest.DefaultShell,
		DefaultEditor:          vcfg.Guest.DefaultEditor,
		WindowManager:          vcfg.Guest.WindowManager,
		GitUserName:            vcfg.Git.UserName,
		GitUserEmail:           vcfg.Git.UserEmail,
		SetDefaultShell:        true,
		BootstrapExtraCommands: extraCommands,
		BootstrapHookScripts:   vcfg.Bootstrap.HookScripts,
		Environment:            vcfg.Environment,
		Image:                  vcfg.Image,
		VoidRepository:         "https://repo-default.voidlinux.org",
		ImageDir:               imageDir,
		BaseImage:              "",
		BaseImageURL:           "",
		SyncPairs:              vcfg.Sync,
		Tunnels:                vcfg.Tunnels,
		Volumes:                vcfg.Volumes,
		PortMappings:           vcfg.PortMappings,
	}
	if cfg.Backend == "utm" || cfg.Backend == "" {
		cfg.UTMBundlePath = utmBundlePath
		cfg.DiskPath = filepath.Join(utmBundlePath, "Data", "disk.img")
		cfg.KernelPath = filepath.Join(utmBundlePath, "Data", "vmlinuz")
		cfg.InitrdPath = filepath.Join(utmBundlePath, "Data", "initramfs.img")
		cfg.MAC = vcfg.Network.MAC
		cfg.StaticIP = vcfg.Network.StaticIP
		cfg.Gateway = vcfg.Network.Gateway
		cfg.CIDR = vcfg.Network.CIDR
		cfg.DNSServers = dnsServersStr
		cfg.GUI = *vcfg.VM.GUI
		cfg.Width = vcfg.VM.Width
		cfg.Height = vcfg.VM.Height
	}
	if cfg.Width == 0 {
		cfg.Width = 1920
	}
	if cfg.Height == 0 {
		cfg.Height = 1200
	}
	if (cfg.Backend == "sbx" || cfg.Backend == "podman") && cfg.Image == "" && cfg.Environment != "" {
		profile, err := ResolveProfile(cfg.ConfigDir, cfg.Environment)
		if err == nil {
			cfg.Image = ProfileImageName(profile)
		}
	}

	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func Usage(cfg Config) string {
	return fmt.Sprintf(`Usage:
  agent-vm                  # open the web UI
  agent-vm [options] <command>

Options:
  -p, --port <port>         Web UI port (default: 8080, env: VM_MANAGER_PORT)

Commands:
  start      Create missing assets and start the VM
  stop       Stop the VM (UTM, Docker, or Podman)
  destroy    Stop the VM and remove generated VM state and disk files
  status     Show VM state and effective network target
  gui        Open the web VM control panel
  bootstrap  Run the guided bootstrap flow and write bootstrap.done
  ssh        SSH into the guest using the configured static IP
  ip         Print or set the configured guest IP (--set)
  sync       Manage file sync pairs between host and VM
  tunnel     Manage SSH tunnels

Backends:
  utm        Apple Virtualization Framework via UTM (default)
  sbx        Docker Sandboxes
  podman     Podman with libkrun

Configuration: %s/vmctl.yaml
Override with: VMCTL_CONFIG_DIR=/custom/path

Defaults:
  VM:          6 CPU / 6144 MiB RAM / 100 GiB disk / 1920x1200
  Network:     192.168.64.10 / gateway 192.168.64.1
  User:        vm / password dev, root password root
  Shell:       fish, editor: neovim, WM: xfce

`, cfg.ConfigDir)
}

func validateConfig(cfg Config) error {
	switch cfg.Backend {
	case "", "utm":
		validShells := map[string]bool{"fish": true, "zsh": true}
		if !validShells[cfg.DefaultShell] {
			return fmt.Errorf("invalid default_shell %q: must be fish or zsh", cfg.DefaultShell)
		}
		validEditors := map[string]bool{"neovim": true, "helix": true}
		if !validEditors[cfg.DefaultEditor] {
			return fmt.Errorf("invalid default_editor %q: must be neovim or helix", cfg.DefaultEditor)
		}
		validWMs := map[string]bool{"lxqt": true, "xfce": true}
		if !validWMs[cfg.WindowManager] {
			return fmt.Errorf("invalid window_manager %q: must be lxqt or xfce", cfg.WindowManager)
		}
	case "sbx", "podman":
		validShells := map[string]bool{"fish": true, "zsh": true}
		if !validShells[cfg.DefaultShell] {
			return fmt.Errorf("invalid default_shell %q: must be fish or zsh", cfg.DefaultShell)
		}
		validEditors := map[string]bool{"neovim": true, "helix": true}
		if !validEditors[cfg.DefaultEditor] {
			return fmt.Errorf("invalid default_editor %q: must be neovim or helix", cfg.DefaultEditor)
		}
	default:
		return fmt.Errorf("invalid backend %q: must be utm, sbx, or podman", cfg.Backend)
	}
	return nil
}

func SaveConfig(cfg Config) error {
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return err
	}
	yamlPath := filepath.Join(cfg.ConfigDir, "vmctl.yaml")

	vcfg := VMConfigFile{}
	vcfg.Backend = cfg.Backend
	vcfg.VM.Name = cfg.Name
	vcfg.VM.CPUs = cfg.CPUs
	vcfg.VM.MemoryMiB = cfg.MemoryMiB
	vcfg.VM.DiskSize = cfg.DiskSize
	vcfg.VM.GUI = &cfg.GUI
	vcfg.VM.Width = cfg.Width
	vcfg.VM.Height = cfg.Height
	vcfg.Network.StaticIP = cfg.StaticIP
	vcfg.Network.Gateway = cfg.Gateway
	vcfg.Network.CIDR = cfg.CIDR
	vcfg.Network.MAC = cfg.MAC
	vcfg.User.Name = cfg.GuestUser
	vcfg.User.Password = cfg.GuestPassword
	vcfg.User.RootPassword = cfg.RootPassword
	vcfg.User.SSHPublicKey = cfg.SSHPublicKey
	vcfg.Guest.Timezone = cfg.Timezone
	vcfg.Guest.DefaultShell = cfg.DefaultShell
	vcfg.Guest.DefaultEditor = cfg.DefaultEditor
	vcfg.Guest.WindowManager = cfg.WindowManager
	vcfg.Git.UserName = cfg.GitUserName
	vcfg.Git.UserEmail = cfg.GitUserEmail
	vcfg.Environment = cfg.Environment
	vcfg.Image = cfg.Image
	vcfg.Sync = cfg.SyncPairs
	vcfg.Tunnels = cfg.Tunnels
	vcfg.Volumes = cfg.Volumes
	vcfg.PortMappings = cfg.PortMappings

	if cfg.DNSServers != "" {
		vcfg.Network.DNSServers = strings.Fields(cfg.DNSServers)
	}

	vcfg.Bootstrap.HookScripts = cfg.BootstrapHookScripts

	vcfg.applyDefaults()
	return saveVMConfigFile(yamlPath, vcfg)
}
