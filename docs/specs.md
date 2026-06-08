# Agent VM Spec

Updated: 2026-06-06

## 1. Goal

Provide a single-command, reproducible `arm64` Linux development VM on Apple Silicon macOS using `UTM.app` + `utmctl`.

- Distribution: `Void Linux aarch64 glibc`
- Desktop: `xfce` or `lxqt` (X11, configurable)
- Fixed IP: `192.168.64.10`
- SSH target: `ssh vm@192.168.64.10`
- Default user: `vm`
- Default resources: `6 CPU / 6 GiB RAM / 100 GiB disk`
- Entry point: `go run ./cmd/agent-vm start`
- Config: `~/.config/agent-vm/vmctl.yaml` (YAML)

Expected outcome:

- first boot downloads, builds, boots, and bootstraps automatically
- later boots reuse the existing VM state
- the codebase stays narrow, explicit, and rebuildable

## 2. Project Boundary

### 2.1 Supported Inputs

The project supports only:

1. The official Void `ROOTFS tarball`
2. Existing disk images: `.img`, `.img.xz`, `.raw`, `.raw.xz`, `.qcow2`

The default implementation:

- download the official `ROOTFS`
- build the disk offline on the host via podman
- extract `vmlinuz` and `initramfs`
- generate a UTM bundle (`.utm`) with `config.plist`
- boot with `utmctl start`

### 2.2 Unsupported Paths

- installer ISO, cloud-init, interactive installers
- board-firmware-oriented ARM images

## 3. Configuration

### 3.1 Config File

All configuration lives in `~/.config/agent-vm/vmctl.yaml`. Override directory with `VMCTL_CONFIG_DIR`.

The YAML schema is defined in `internal/vmctl/yaml_config.go`. Every key is optional — defaults apply for anything omitted.

```yaml
vm:
  name: void-dev
  cpus: 6
  memory_mib: 6144
  disk_size: "100G"
  gui: true
  width: 1920
  height: 1200

network:
  static_ip: "192.168.64.10"
  gateway: "192.168.64.1"
  cidr: 24
  dns_servers: ["1.1.1.1", "8.8.8.8"]
  mac: "52:54:00:64:00:10"

user:
  name: vm
  password: dev
  root_password: root
  ssh_public_key: ""          # blank = auto-detect ~/.ssh/id_ed25519.pub

guest:
  timezone: Australia/Sydney
  default_shell: fish
  default_editor: neovim
  window_manager: xfce

bootstrap:
  hook_scripts:
    - ~/.config/agent-vm/hooks/install-rust.sh
    - ~/.config/agent-vm/hooks/install-packages.sh

git:
  user_name: ""
  user_email: ""

sync:
  - name: myproject
    host_path: /Users/me/projects/myproject
    target_path: /home/vm/myproject
    mode: copy
    direction: host-to-vm
    exclude: [node_modules, .git]

tunnels:
  - name: webapp
    type: local
    local_port: 3000
    remote_port: 3000
    enabled: true
    auto_start: true
```

### 3.2 Directory Layout

```
~/.config/agent-vm/
├── vmctl.yaml
├── scripts/
│   └── guest-bootstrap.sh   # generated at bootstrap time
├── images/                  # base Void rootfs tarballs
└── void-dev.utm/            # UTM bundle
    ├── config.plist
    └── Data/
        ├── disk.img
        ├── vmlinuz
        ├── initramfs.img
        └── bootstrap.done
```

## 4. Bootstrap

### 4.1 Bootstrap Flow

Bootstrap runs automatically on first boot. It is a shell script generated from `internal/vmctl/bootstrap_script.go` and written to `~/.config/agent-vm/scripts/guest-bootstrap.sh` at bootstrap time.

Bootstrap completion is tracked by a `bootstrap.done` marker. Subsequent VM starts skip bootstrap.

### 4.2 Bootstrap Scope

The agent-vm bootstrap installs only essential infrastructure:
- Homebrew for Linux
- Docker with docker-compose and docker-buildx plugins
- Selected shell (fish/zsh), editor (neovim/helix), and window manager (xfce/lxqt)
- fnm + Node.js
- Browsers (Chromium, Zen Browser)
- Fcitx5 Chinese input, Ghostty terminal
- spice-vdagent for clipboard sharing via UTM/SPICE

All other developer tools (Rust, zellij, zig, lazygit, gitui, opencode, cargo crates, etc.) should be installed via user-provided hook scripts.

### 4.3 Post-Bootstrap Hook Scripts

- **hook_scripts**: YAML list of paths to shell scripts on the host. Each file is read and its content is appended and executed as a post-bootstrap hook inside the guest. These do not run on subsequent VM restarts.
- CLI: `agent-vm bootstrap --hook script1.sh --hook script2.sh` (repeatable `--hook` flag)

## 5. Boot And System Behavior

### 5.1 First Boot

Running `go run ./cmd/agent-vm start` must automatically:

1. download the official Void rootfs into `~/.config/agent-vm/images/`
2. build a raw disk via podman
3. write users, networking, SSH, GUI, and system config offline
4. extract `vmlinuz` and `initramfs`
5. generate UTM bundle with `config.plist`
6. start the VM via `utmctl start`
7. wait for SSH and run bootstrap once

### 5.2 Later Boots

If the disk and boot assets already exist:

- boot directly into the existing VM
- do not rerun bootstrap

## 6. Users, Login, And GUI

### 6.1 Default Accounts

- User: `vm` / password: `dev`
- Root: `root` / password: `root`
- SSH key: auto-detected from `~/.ssh/id_ed25519.pub`; override via `user.ssh_public_key` in YAML

### 6.2 SSH

```bash
ssh vm@192.168.64.10
```

### 6.3 GUI Session

- `tty1 autologin -> vm -> X11 desktop (xfce or lxqt)`
- Boot directly into the user desktop session without a display manager
- `vmctl-session` wrapper launches `startxfce4` or `startlxqt` with X11 environment variables
- `gui: false` in YAML boots headless (no display window)
- Clipboard sharing is handled natively by UTM via SPICE + `spice-vdagent`

## 7. Networking

- UTM shared networking (virtio-net-pci)
- Guest fixed IP: `192.168.64.10/24`
- Default gateway: `192.168.64.1`
- Fixed IP binds to virtual NIC MAC address

## 8. Sync

File sync between host and VM, configured in `vmctl.yaml` under `sync:` or via CLI/web UI.

**copy** mode: rsync with configurable backups.
**git** mode: creates a bare repo on the VM, adds a `vm` remote on the host. Host pushes/pulls via `git push vm` / `git pull vm`. The VM target directory is cloned from the bare repo.

## 9. Tunnels

SSH port forwarding managed under `tunnels:` in `vmctl.yaml`. Supports local and remote forwarding with auto-start on VM boot.

## 10. Web UI

Running `go run ./cmd/agent-vm` without a subcommand starts the web UI on port 8080 (`VM_MANAGER_PORT`).

The UI provides:
- Bootstrap configuration (shell, editor, WM, hook scripts, git identity)
- VM start/stop/destroy with progress streaming
- Guest CPU/memory metrics
- Sync pair management
- Tunnel management

## 11. Guest Software Set

### 11.1 Base System

- `linux6.12`, `dracut`, `NetworkManager`, `dbus`, `openssh`, `curl`, `wget`, `git`, `sudo`, `chrony`

### 11.2 GUI And Desktop

- `xfce4` or `lxqt` + `openbox`, `xorg`, `mesa`, `mesa-dri`, `ghostty`, `spice-vdagent`

### 11.3 Development Environment

- `fish` or `zsh`, `neovim` or `helix`, Homebrew for Linux, `fnm`, `chromium`, `zen-browser`

## 12. Acceptance Criteria

1. `go run ./cmd/agent-vm start` completes download, disk build, and boot
2. The GUI automatically enters the configured desktop session
3. The host can `ssh vm@192.168.64.10`
4. `bootstrap.done` prevents an unexpected second bootstrap
5. `fish/zsh`, `ghostty`, Homebrew, Helix/Neovim, Chromium, Zen Browser, and Fcitx5 are all present
6. Post-bootstrap hooks execute once and not on restart
7. Sync pairs and tunnels persist in `vmctl.yaml`
8. Web UI reflects config changes without server restart
9. Clipboard works between host and guest via UTM/SPICE
