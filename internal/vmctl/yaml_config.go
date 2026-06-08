package vmctl

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// VMConfigFile is the on-disk YAML schema for ~/.config/agent-vm/vmctl.yaml
type VMConfigFile struct {
	Backend      string          `yaml:"backend"`
	VM           VMConfig        `yaml:"vm"`
	Network      NetworkConfig   `yaml:"network"`
	User         UserConfig      `yaml:"user"`
	Guest        GuestConfig     `yaml:"guest"`
	Bootstrap    BootstrapConfig `yaml:"bootstrap"`
	Git          GitConfig       `yaml:"git"`
	Environment  string          `yaml:"environment"`
	Image        string          `yaml:"image"`
	Sync         []SyncPair      `yaml:"sync"`
	Tunnels      []Tunnel        `yaml:"tunnels"`
	Volumes      []VolumeMount   `yaml:"volumes"`
	PortMappings []PortMapping   `yaml:"ports"`
}

type VMConfig struct {
	Name      string `yaml:"name"`
	CPUs      int    `yaml:"cpus"`
	MemoryMiB int    `yaml:"memory_mib"`
	DiskSize  string `yaml:"disk_size"`
	GUI       *bool  `yaml:"gui"`
	Width     int    `yaml:"width"`
	Height    int    `yaml:"height"`
}

type NetworkConfig struct {
	StaticIP   string   `yaml:"static_ip"`
	Gateway    string   `yaml:"gateway"`
	CIDR       int      `yaml:"cidr"`
	DNSServers []string `yaml:"dns_servers"`
	MAC        string   `yaml:"mac"`
}

type UserConfig struct {
	Name         string `yaml:"name"`
	Password     string `yaml:"password"`
	RootPassword string `yaml:"root_password"`
	SSHPublicKey string `yaml:"ssh_public_key"`
}

type GuestConfig struct {
	Timezone      string `yaml:"timezone"`
	DefaultShell  string `yaml:"default_shell"`
	DefaultEditor string `yaml:"default_editor"`
	WindowManager string `yaml:"window_manager"`
}

type BootstrapConfig struct {
	HookScripts []string `yaml:"hook_scripts"`
}

type GitConfig struct {
	UserName  string `yaml:"user_name"`
	UserEmail string `yaml:"user_email"`
}

func determineConfigDir() (string, error) {
	if dir := os.Getenv("VMCTL_CONFIG_DIR"); dir != "" {
		return filepath.Clean(dir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "agent-vm"), nil
}

func configYAMLPath() (string, error) {
	dir, err := determineConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vmctl.yaml"), nil
}

func scriptsDir() (string, error) {
	dir, err := determineConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scripts"), nil
}

func defaultImageDir() (string, error) {
	dir, err := determineConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "images"), nil
}

func defaultStateDir(name string) (string, error) {
	dir, err := determineConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func loadVMConfigFile(path string) (VMConfigFile, error) {
	var cfg VMConfigFile
	cfg.applyDefaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *VMConfigFile) applyDefaults() {
	if c.Backend == "" {
		c.Backend = "utm"
	}
	if c.VM.Name == "" {
		c.VM.Name = "void-dev"
	}
	if c.VM.CPUs == 0 {
		c.VM.CPUs = 6
	}
	if c.VM.MemoryMiB == 0 {
		c.VM.MemoryMiB = 6144
	}
	if c.VM.DiskSize == "" {
		c.VM.DiskSize = "100G"
	}
	if c.VM.GUI == nil {
		t := true
		c.VM.GUI = &t
	}
	if c.Backend == "utm" || c.Backend == "" {
		if c.Network.StaticIP == "" {
			c.Network.StaticIP = "192.168.64.10"
		}
		if c.Network.Gateway == "" {
			c.Network.Gateway = "192.168.64.1"
		}
		if c.Network.CIDR == 0 {
			c.Network.CIDR = 24
		}
		if len(c.Network.DNSServers) == 0 {
			c.Network.DNSServers = []string{"1.1.1.1", "8.8.8.8"}
		}
		if c.Network.MAC == "" {
			c.Network.MAC = "52:54:00:64:00:10"
		}
	}
	if c.User.Name == "" {
		c.User.Name = "vm"
	}
	if c.User.Password == "" {
		c.User.Password = "dev"
	}
	if c.User.RootPassword == "" {
		c.User.RootPassword = "root"
	}
	if c.Guest.Timezone == "" {
		c.Guest.Timezone = "Australia/Sydney"
	}
	if c.Guest.DefaultShell == "" {
		c.Guest.DefaultShell = "fish"
	}
	if c.Guest.DefaultEditor == "" {
		c.Guest.DefaultEditor = "neovim"
	}
	if c.Backend == "utm" || c.Backend == "" {
		if c.Guest.WindowManager == "" {
			c.Guest.WindowManager = "xfce"
		}
	}
}

func saveVMConfigFile(path string, cfg VMConfigFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
