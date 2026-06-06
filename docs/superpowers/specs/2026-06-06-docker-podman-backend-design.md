# Docker Sandbox & Podman libkrun Backend Design

Date: 2026-06-06

## Goal

Add Docker sandbox (`sbx`) and Podman (libkrun driver) as alternative backends to UTM for running lightweight, headless Linux development environments. These backends use pre-built environment images (Alpine-based) with no GUI, no SSH daemon — all operations go through `sbx exec` / `podman exec`.

## Decisions

| Decision | Choice |
|---|---|
| Docker backend | `sbx` CLI (Docker Sandboxes) — microVM isolation |
| Podman backend | `podman` CLI with libkrun VM driver |
| Container operations | `sbx exec` / `podman exec` only — no SSH inside container |
| Environment images | Pre-built Alpine images per language (Go, Node, PHP, Python, base) |
| Rootfs security | `--read-only` + tmpfs for /tmp, /run, /var/tmp |
| Persistence | Named volumes for `/home/vm` and `/home/linuxbrew/.linuxbrew` |
| File sharing | `-v` bind mounts at container start |
| Port forwarding | `-p` port mapping (podman) / `sbx ports --publish` (sbx) |
| Multi-container | One config = one container. Multiple projects use separate config dirs |
| Runtime package installs | Via Homebrew (persisted) or language package managers (npm, pip, go install → user home) |
| Architecture approach | Backend interface + dispatch |

## Pre-built Environment Images

Images live in `images/` directory as Dockerfiles. Built with `agent-vm build-image` or pulled from registry.

### Available environments

| Image | Contents |
|---|---|
| `agent-vm-base` | Alpine + bash + fish/zsh + neovim/helix + git + sudo + Homebrew + fnm + Docker CLI + compose + buildx |
| `agent-vm-go` | base + Go latest + gopls + dlv + staticcheck |
| `agent-vm-node` | base + Node.js LTS (fnm) + pnpm + yarn |
| `agent-vm-php` | base + PHP + Composer + Symfony CLI + phpunit |
| `agent-vm-python` | base + Python 3 + pip + uv + venv |

### Image structure (Dockerfile template)

```dockerfile
FROM alpine:latest

# System packages
RUN apk add --no-cache bash git curl wget sudo ca-certificates tzdata \
    build-base <shell-pkg> <editor-pkg>

# User: non-root, no root login
RUN adduser -D -s /bin/<shell> vm && \
    echo "vm:ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vm && \
    passwd -l root

# Timezone
RUN cp /usr/share/zoneinfo/<timezone> /etc/localtime && \
    echo "<timezone>" > /etc/timezone

# Homebrew (installed to /home/linuxbrew)
RUN /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /home/vm/.profile

# fnm + Node.js LTS
USER vm
RUN curl -fsSL https://fnm.vercel.app/install | bash && \
    /home/vm/.local/share/fnm/fnm install --lts

# Docker CLI + compose + buildx
USER root
RUN apk add --no-cache docker-cli && \
    mkdir -p /usr/libexec/docker/cli-plugins && \
    curl -fsSL ... -o /usr/libexec/docker/cli-plugins/docker-compose && \
    curl -fsSL ... -o /usr/libexec/docker/cli-plugins/docker-buildx

# <environment-specific packages>
# e.g. for Go: RUN go install golang.org/x/tools/gopls@latest ...

# Git config
USER vm
RUN git config --global core.editor <editor> && \
    git config --global init.defaultBranch main && \
    git config --global pull.rebase false && \
    git config --global push.autoSetupRemote true

# Default directories
RUN mkdir -p /home/vm/repos /home/vm/projects

# Entrypoint
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
```

Entrypoint (`entrypoint.sh`):
```bash
#!/bin/sh
# Initialize named volumes on first run
for vol in /home/vm /home/linuxbrew/.linuxbrew; do
  if [ ! -f "${vol}/.vmctl-initialized" ]; then
    # Volume is empty — nothing to copy, just mark initialized
    touch "${vol}/.vmctl-initialized"
  fi
done
exec "$@"
```

Default CMD: `sleep infinity`

### Build command

```
agent-vm build-image [--env base|go|node|php|python]
```

Generates Dockerfile from config + environment template, builds image locally.

## Config Changes

### New YAML fields

```yaml
backend: utm           # "utm" | "sbx" | "podman"
image: agent-vm-base   # environment image (only for sbx/podman)

# Only used when backend is sbx or podman:
volumes:
  - name: projects                # bind mount for file sharing
    host_path: /Users/me/projects
    mount: /home/vm/projects

ports:
  - host: 3000
    guest: 3000
  - host: 8080
    guest: 80
```

Named volumes are auto-created: `<name>_home` → `/home/vm`, `<name>_brew` → `/home/linuxbrew/.linuxbrew`. Not configurable — always present for container backends.

### Config struct changes

New fields in `Config`:
- `Backend string` — `"utm"`, `"sbx"`, or `"podman"`
- `ContainerName string` — derived from `Name` (e.g., `agent-vm-void-dev`)
- `Image string` — environment image name
- `Volumes []VolumeMount` — user-defined bind mounts
- `PortMappings []PortMapping` — host:guest port pairs

New types:
```go
type VolumeMount struct {
    Name     string
    HostPath string // empty for named volumes, set for bind mounts
    Mount    string
}

type PortMapping struct {
    Host  int
    Guest int
}
```

### Validation

`validateConfig()` branches on `cfg.Backend`:
- **UTM**: validates WM (lxqt/xfce), network fields, checks `utmctl` binary
- **sbx**: checks `sbx` binary, validates image exists
- **podman**: checks `podman` binary, validates image exists, validates port mappings, validates libkrun driver

`WindowManager` validation only runs when `Backend == "utm"`.

## Backend Interface

### Definition (`internal/vmctl/backend.go`)

```go
type Backend interface {
    Start(cfg Config) error
    Stop(cfg Config) error
    Destroy(cfg Config) error
    IsRunning() (bool, error)
    Status() (VMStatus, error)
    Exec(cfg Config, args ...string) error
    BootstrapSetup(cfg Config) error
}
```

Factory:
```go
func NewBackend(cfg Config) Backend
```

### Implementations

| Backend | CLI | Key differences |
|---|---|---|
| `UTMBackend` | `utmctl` | SSH-based operations, GUI VM |
| `SBXBackend` | `sbx` | `sbx create/exec/stop/rm/ports`, microVM isolation |
| `PodmanBackend` | `podman` | `podman run/exec/stop/rm`, libkrun VM driver, `--read-only` + security flags |

### UTMBackend

Wraps existing UTM/utmctl lifecycle. Moves current free functions into struct methods. No behavior change.

### SBXBackend

Uses `sbx` CLI (Docker Sandboxes):
- `sbx create --name <name> .` — create sandbox with workspace
- `sbx exec <name> <cmd>` — run commands inside
- `sbx stop <name>` — stop sandbox
- `sbx rm <name>` — remove sandbox
- `sbx ports <name> --publish host:guest` — port forwarding
- Bootstrap: run setup script via `sbx exec`
- Persistence: sbx sandboxes persist state across stops; removed on `sbx rm`

### PodmanBackend

Uses `podman` CLI with security hardening:
- `podman run` with `--read-only`, `--cap-drop=ALL`, `--security-opt no-new-privileges`
- Named volumes for `/home/vm` and `/home/linuxbrew/.linuxbrew`
- `-p` for port mapping, `-v` for bind mounts
- `podman exec` for running commands
- Verifies libkrun driver on first start

## Podman Security Model

### Layer 1: VM isolation (libkrun)
- Each container runs in its own microVM via libkrun
- Separate kernel, separate memory space
- Container escape requires VM escape — fundamentally different security boundary

### Layer 2: Container hardening
```
--read-only                           # immutable rootfs
--cap-drop=ALL                        # no Linux capabilities
--security-opt no-new-privileges      # no setuid escalation
--pids-limit 4096                     # fork bomb protection
--tmpfs /tmp:rw,noexec,nosuid         # temp, no exec
--tmpfs /run:rw,noexec,nosuid         # runtime sockets
--tmpfs /var/tmp:rw,noexec,nosuid     # temp
```

### Layer 3: No root access
- Image disables root login (`passwd -l root`)
- Container runs as user `vm`
- sudo available for specific operations but no root shell

### Layer 4: Persistent writable paths (named volumes only)
- `agent-vm-<name>_home` → `/home/vm` (user data, configs, projects)
- `agent-vm-<name>_brew` → `/home/linuxbrew/.linuxbrew` (Homebrew packages)
- User-defined bind mounts for project files

### Runtime package installation
- No `apk` at runtime (rootfs is read-only)
- Homebrew (`brew install`) → persisted in brew volume
- Language package managers (`go install`, `npm i -g`, `pip install`) → go to user home volume
- These persist across container recreations

## Container Lifecycle

### SBXBackend.Start()

1. Check `sbx` binary exists
2. Check if sandbox already exists: `sbx ls`
3. If not exists: `sbx create --name <name> .`
4. `sbx run <name>` (starts sandbox)
5. If no bootstrap marker: run setup via `sbx exec`, write marker
6. Set up port forwarding: `sbx ports <name> --publish host:guest`

### SBXBackend.Stop()

1. `sbx stop <name>`

### SBXBackend.Destroy()

1. `sbx rm <name>`
2. Remove state dir

### SBXBackend.Exec()

1. `sbx exec -it <name> <args...>`

### PodmanBackend.Start()

1. Check `podman` binary exists, verify libkrun driver
2. Check if image exists: `podman image inspect <image>`
3. If not: return error (user must `build-image` first)
4. Check if container exists: `podman inspect <containerName>`
5. If container exists but stopped: `podman start <containerName>`
6. If no container: `podman run -d --name <containerName> --read-only --cap-drop=ALL --security-opt no-new-privileges --pids-limit 4096 --tmpfs /tmp:rw,noexec,nosuid --tmpfs /run:rw,noexec,nosuid --tmpfs /var/tmp:rw,noexec,nosuid -v <name>_home:/home/vm -v <name>_brew:/home/linuxbrew/.linuxbrew [-p ports] [-v volumes] <image>`
7. If no bootstrap marker: run hook scripts via `podman exec`, write marker
8. Start auto-tunnel equivalents (port mappings)

### PodmanBackend.Stop()

1. `podman stop <containerName>`

### PodmanBackend.Destroy()

1. `podman rm -f <containerName>`
2. Remove state dir
3. Named volumes preserved by default

### PodmanBackend.Exec()

1. `podman exec -it -u vm --workdir /home/vm <containerName> <args...>`

### PodmanBackend.Status()

1. `podman inspect` for container state
2. `podman stats --no-stream` for CPU/memory metrics

## Sync Changes

### UTM backend (unchanged)

- Copy mode: rsync over SSH
- Git mode: git over SSH

### Container backends

- **Volume mounts** (`volumes:` in YAML): `-v host_path:container_path` bind mount. Files are live-shared. Primary file-sharing mechanism.
- **Git mode**: `podman exec` / `sbx exec` for git operations
- No rsync/SSH-based sync

## Tunnel Changes

### UTM backend (unchanged)

SSH-based tunnels via background SSH processes with PID files.

### Container backends

- **Podman**: `-p host:guest` port mapping at `podman run` time. Adding/removing requires container recreation.
- **SBX**: `sbx ports --publish host:guest` post-start. Dynamic, no recreation needed.
- Web UI "Tunnels" panel becomes "Port Mappings" for container backends

## Web UI Changes

### Backend selector

Dropdown at top of bootstrap modal: `UTM`, `Docker (sbx)`, `Podman`. Changing it dynamically shows/hides fields.

### Bootstrap modal — conditional fields

| Field | UTM | SBX/Podman |
|-------|-----|------------|
| Shell | shown | shown (for image build) |
| Editor | shown | shown (for image build) |
| Window Manager | shown | **hidden** |
| Memory (MiB) | shown | shown |
| Disk Size | shown | **hidden** |
| Guest IP | shown | **hidden** |
| Environment Image | **hidden** | shown (dropdown: base, go, node, php, python) |
| Hook Scripts | shown | shown |
| Git name/email | shown | shown |
| Port Mappings | **hidden** | shown |
| Volume Mounts | **hidden** | shown |

### Tunnels panel

- UTM: current SSH-based tunnel UI
- SBX/Podman: port mappings list

### Status display

- UTM: VM state, PID, IP, disk path
- SBX: sandbox state, agent, ports
- Podman: container state, image, ports, volumes

## Build Image Command

```
agent-vm build-image [--env base|go|node|php|python]
```

1. Generate Dockerfile from config + environment template
2. `podman build -t agent-vm-<env> <stateDir>/` (or `docker build`)
3. Tag image

Users can also bring their own image — just set `image: my-custom-image` in config.

## Files Changed

| File | Change |
|---|---|
| `internal/vmctl/config.go` | Add `Backend`, `Image`, `ContainerName`, `Volumes`, `PortMappings`; backend-aware validation |
| `internal/vmctl/yaml_config.go` | Parse `backend:`, `image:`, `volumes:`, `ports:` |
| `internal/vmctl/backend.go` | **New** — `Backend` interface, `NewBackend()` factory |
| `internal/vmctl/backend_utm.go` | **New** — `UTMBackend` (moves code from vm.go, inspect.go) |
| `internal/vmctl/backend_sbx.go` | **New** — `SBXBackend` (sbx CLI) |
| `internal/vmctl/backend_podman.go` | **New** — `PodmanBackend` (podman CLI + security) |
| `internal/vmctl/image.go` | **New** — Dockerfile generation, `BuildImage()` |
| `images/base/Dockerfile` | **New** — base environment image |
| `images/go/Dockerfile` | **New** — Go environment image |
| `images/node/Dockerfile` | **New** — Node.js environment image |
| `images/php/Dockerfile` | **New** — PHP environment image |
| `images/python/Dockerfile` | **New** — Python environment image |
| `internal/vmctl/vm.go` | Trimmed — delegates to backend |
| `internal/vmctl/inspect.go` | Trimmed — delegates to backend |
| `internal/vmctl/cobra.go` | `build-image` command, backend-aware commands |
| `internal/vmctl/web_handlers.go` | Backend-aware handlers |
| `web/static/app.js` | Conditional UI, backend selector, image dropdown |
| `web/static/index.html` | Backend selector, port/volume inputs |

## Acceptance Criteria

1. `backend: utm` produces identical behavior to current system
2. `backend: sbx` creates/manages sandboxes via `sbx` CLI
3. `backend: podman` runs containers with `--read-only --cap-drop=ALL --security-opt no-new-privileges`
4. Podman verifies libkrun driver on first start
5. No SSH daemon inside containers
6. `sbx exec` / `podman exec` for all container operations
7. Named volumes persist `/home/vm` and `/home/linuxbrew/.linuxbrew` across recreations
8. Pre-built environment images available: base, go, node, php, python
9. `build-image` command generates and builds images
10. Port mappings in config map to `-p` / `sbx ports`
11. Volume mounts in config map to `-v` bind mounts
12. Web UI hides WM/desktop when backend is sbx/podman
13. Web UI shows environment image selector, port mappings, volume mounts
14. Root login disabled in container images
15. `go build ./...` and `go test ./internal/vmctl/...` pass
