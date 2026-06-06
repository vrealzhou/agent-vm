# Docker Sandbox & Podman libkrun Backend Design

Date: 2026-06-06

## Goal

Add Docker sandbox and Podman (libkrun driver) as alternative backends to UTM for running lightweight, headless Linux development environments. These backends use Alpine-based containers with no GUI, no SSH daemon — all operations go through `docker exec` / `podman exec`.

## Decisions

| Decision | Choice |
|---|---|
| Container operations | `docker exec` / `podman exec` only — no SSH inside container |
| Base image | Alpine, via generated Dockerfile |
| Bootstrap scope | Full bootstrap in Dockerfile (shell, editor, git, Homebrew, fnm+Node, Docker CLI tools) |
| Brew persistence | Docker named volume mounted at `/home/linuxbrew/.linuxbrew` |
| File sharing | Docker/Podman `-v` bind mounts |
| Port forwarding | Docker/Podman `-p` port mapping (no SSH tunnels) |
| Podman runtime | podman with libkrun as VM driver (`podman machine set --driver libkrun`) |
| Architecture approach | Backend interface + dispatch (Approach A) |

## Config Changes

### New YAML fields

```yaml
backend: utm  # "utm" | "docker" | "podman"

# Only used when backend is docker or podman:
volumes:
  - name: homebrew          # named volume for brew persistence
    mount: /home/linuxbrew/.linuxbrew
  - name: projects          # bind mount for file sharing
    host_path: /Users/me/projects
    mount: /home/vm/projects

ports:
  - host: 3000
    guest: 3000
  - host: 8080
    guest: 80
```

### Config struct changes

New fields in `Config`:
- `Backend string` — `"utm"`, `"docker"`, or `"podman"`
- `ContainerName string` — derived from `Name` (e.g., `agent-vm-void-dev`)
- `Volumes []VolumeMount` — named volumes and bind mounts
- `PortMappings []PortMapping` — host:guest port pairs

New types:
```go
type VolumeMount struct {
    Name     string // volume name
    HostPath string // empty for named volumes, set for bind mounts
    Mount    string // container mount point
}

type PortMapping struct {
    Host  int
    Guest int
}
```

Existing UTM-only fields (`UTMBundlePath`, `KernelPath`, `InitrdPath`, `DiskPath`, `GUI`, `Width`, `Height`, `MAC`, `StaticIP`, `Gateway`, `CIDR`) remain in the struct but are only populated when `Backend == "utm"`.

### Validation

`validateConfig()` branches on `cfg.Backend`:
- **UTM**: validates WM (lxqt/xfce), network fields, checks `utmctl` binary
- **Docker**: checks `docker` binary, validates port mappings, validates volume mounts
- **Podman**: checks `podman` binary, validates port mappings, validates volume mounts

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

Returns `&UTMBackend{}`, `&DockerBackend{}`, or `&PodmanBackend{}`.

### UTMBackend

Wraps the existing UTM/utmctl lifecycle. Moves current free functions (`Start`, `Stop`, `Destroy` from `vm.go`, `InspectVM` from `inspect.go`) into struct methods. No behavior change.

### ContainerBackend (shared for Docker + Podman)

`DockerBackend` and `PodmanBackend` share ~95% of code. Implementation:

```go
type ContainerBackend struct {
    cli string // "docker" or "podman"
}

func (cb *ContainerBackend) Start(cfg Config) error
func (cb *ContainerBackend) Stop(cfg Config) error
func (cb *ContainerBackend) Destroy(cfg Config) error
func (cb *ContainerBackend) IsRunning() (bool, error)
func (cb *ContainerBackend) Status() (VMStatus, error)
func (cb *ContainerBackend) Exec(cfg Config, args ...string) error
func (cb *ContainerBackend) BootstrapSetup(cfg Config) error
```

`PodmanBackend` is `ContainerBackend{cli: "podman"}` with an additional check on first start that verifies the podman machine is using the libkrun driver.

## Container Lifecycle

### Start()

1. Check `docker`/`podman` binary exists
2. Generate Dockerfile from config to `~/.config/agent-vm/<name>/Dockerfile` (if not exists or config changed)
3. `docker build -t <containerName> <stateDir>/`
4. `docker run -d --name <containerName> [port flags] [volume flags] <containerName>`
   - Port flags: `-p 3000:3000 -p 8080:80` for each `PortMapping`
   - Volume flags: `-v homebrew:/home/linuxbrew/.linuxbrew` for named volumes, `-v /host/path:/container/path` for bind mounts
5. If no `bootstrap.done` marker: run hook scripts via `docker exec`, write marker
6. Store container ID in state dir

### Stop()

1. `docker stop <containerName>`

### Destroy()

1. `docker rm -f <containerName>`
2. Optionally `docker rmi <containerName>` (remove image)
3. Remove state dir
4. Named volumes are preserved by default (not destroyed)

### IsRunning()

1. `docker inspect --format='{{.State.Running}}' <containerName>`

### Exec()

1. `docker exec -it <containerName> <args...>`

### Status()

1. `docker inspect` for container state (running, stopped, etc.)
2. `docker stats --no-stream` for CPU/memory metrics

### BootstrapSetup()

1. Check if image already built and marker exists — skip if so
2. Generate Dockerfile (see below)
3. Build image
4. Run container
5. Execute hook scripts via `docker exec bash -c '<script>'`
6. Write `bootstrap.done` marker

## Dockerfile Generation

### File: `internal/vmctl/dockerfile.go`

Generates a Dockerfile from config, written to `~/.config/agent-vm/<name>/Dockerfile`.

Template structure:

```dockerfile
FROM alpine:latest

# System packages
RUN apk add --no-cache bash git curl wget sudo ca-certificates \
    <shell-package> <editor-package> make gcc docker-cli

# User setup
RUN adduser -D -s /bin/<shell> vm && \
    echo "vm:dev" | chpasswd && \
    addgroup vm docker 2>/dev/null || true
RUN echo "root:root" | chpasswd

# Timezone
RUN apk add --no-cache tzdata && \
    cp /usr/share/zoneinfo/<timezone> /etc/localtime && \
    echo "<timezone>" > /etc/timezone

# Homebrew
RUN /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# fnm + Node.js LTS
USER vm
RUN curl -fsSL https://fnm.vercel.app/install | bash && \
    /home/vm/.local/share/fnm/fnm install --lts
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/vm/.local/share/fnm:$PATH"

# Docker compose + buildx plugins
USER root
RUN mkdir -p /usr/libexec/docker/cli-plugins && \
    curl -fsSL "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-aarch64" \
      -o /usr/libexec/docker/cli-plugins/docker-compose && \
    chmod 0755 /usr/libexec/docker/cli-plugins/docker-compose && \
    curl -fsSL "https://github.com/docker/buildx/releases/latest/download/buildx-v0.24.0.linux-arm64" \
      -o /usr/libexec/docker/cli-plugins/docker-buildx && \
    chmod 0755 /usr/libexec/docker/cli-plugins/docker-buildx

# Git config
USER vm
RUN git config --global core.editor <editor-cmd> && \
    git config --global init.defaultBranch main && \
    git config --global pull.rebase false && \
    git config --global push.autoSetupRemote true
    [optional: user.name / user.email]

# Default directories
RUN mkdir -p /home/vm/repos /home/vm/projects

# Entrypoint — keep container alive
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
```

`entrypoint.sh` (generated alongside Dockerfile in `<stateDir>/entrypoint.sh`):
```bash
#!/bin/sh
sleep infinity
```

### Package mapping

| Config value | Alpine package |
|---|---|
| `default_shell: fish` | `fish` |
| `default_shell: zsh` | `zsh` |
| `default_editor: neovim` | `neovim` |
| `default_editor: helix` | `helix` |

### Config change detection

Store a hash of the config used to generate the Dockerfile. If the hash changes, regenerate and rebuild. Hash is stored in `<stateDir>/dockerfile.hash`.

## Sync Changes

### UTM backend (unchanged)

- Copy mode: rsync over SSH
- Git mode: git over SSH

### Docker/Podman backend

- **Volume mode** (new): `-v host_path:container_path` bind mount at `docker run` time. Files are live-shared. No rsync needed. This is the primary file-sharing mechanism for container backends, configured via the `volumes:` YAML section.
- **Copy mode**: `docker cp` instead of rsync over SSH
- **Git mode**: `docker exec` instead of SSH for git operations
- The existing `sync:` section still works for git mode (via `docker exec`)
- When backend is docker/podman: `volumes:` defines Docker bind mounts, `sync:` defines git-mode sync pairs

## Tunnel Changes

### UTM backend (unchanged)

SSH-based tunnels via background SSH processes with PID files.

### Docker/Podman backend

- Tunnels are replaced by `ports:` config → Docker `-p` port mapping
- Port mappings are set at `docker run` time
- Adding/removing ports requires stopping and recreating the container (`docker stop` + `docker rm` + `docker run` with updated ports)
- Web UI "Tunnels" panel becomes "Port Mappings" for container backends
- Auto-start is implicit — ports are always mapped when container runs
- The `Tunnel` struct and `tunnel_manager.go` code is not used for container backends

## Web UI Changes

### Backend selector

Add a backend dropdown at the top of the bootstrap modal: `UTM`, `Docker`, `Podman`. Changing it dynamically shows/hides relevant fields.

### Bootstrap modal — conditional fields

| Field | UTM | Docker/Podman |
|-------|-----|---------------|
| Shell | shown | shown |
| Editor | shown | shown |
| Window Manager | shown | **hidden** |
| Memory (MiB) | shown | shown |
| Disk Size | shown | **hidden** |
| Guest IP | shown | **hidden** |
| Hook Scripts | shown | shown |
| Git name/email | shown | shown |

New fields for Docker/Podman:
- **Port mappings**: editable list of host:guest port pairs (add/remove rows)
- **Volume mounts**: editable list of host_path:container_path pairs + named volumes

### Tunnels panel

- UTM: current SSH-based tunnel UI (unchanged)
- Docker/Podman: shows port mappings with note "Managed via container port mapping". Add/remove triggers container recreation.

### Status display

- UTM: VM state, PID, IP, disk path
- Docker/Podman: container state, image name, port mappings, volumes

### Implementation

Frontend checks `config.backend` from the status API response. Conditional rendering via CSS classes or conditional DOM construction in `app.js`.

## Files Changed

| File | Change |
|---|---|
| `internal/vmctl/config.go` | Add `Backend`, `ContainerName`, `Volumes`, `PortMappings` fields; backend-aware validation |
| `internal/vmctl/yaml_config.go` | Parse `backend:`, `volumes:`, `ports:` from YAML |
| `internal/vmctl/backend.go` | **New** — `Backend` interface, `NewBackend()` factory |
| `internal/vmctl/backend_utm.go` | **New** — `UTMBackend` implementation (moves code from vm.go, inspect.go) |
| `internal/vmctl/backend_container.go` | **New** — `ContainerBackend` (shared Docker/Podman) |
| `internal/vmctl/dockerfile.go` | **New** — Dockerfile generation from config |
| `internal/vmctl/vm.go` | Trimmed — lifecycle functions move to backend implementations |
| `internal/vmctl/inspect.go` | Trimmed — inspect functions move to backend implementations |
| `internal/vmctl/cobra.go` | Update commands to use `NewBackend(cfg)` |
| `internal/vmctl/web_handlers.go` | Backend-aware status/bootstrap/tunnel handlers |
| `web/static/app.js` | Conditional UI based on backend type |
| `web/static/index.html` | Backend selector, port/volume inputs |
| `internal/vmctl/config_test.go` | Tests for new config fields and validation |
| `internal/vmctl/yaml_config_test.go` | Tests for new YAML parsing |
| `internal/vmctl/dockerfile_test.go` | **New** — Dockerfile generation tests |
| `internal/vmctl/backend_container_test.go` | **New** — ContainerBackend tests |

## Acceptance Criteria

1. `backend: utm` in config produces identical behavior to current system
2. `backend: docker` generates a Dockerfile, builds image, runs container
3. `backend: podman` works identically to docker backend with `podman` CLI, verifies libkrun driver
4. No SSH daemon runs inside Docker/Podman containers
5. `docker exec` / `podman exec` used for all container operations (bootstrap hooks, git sync, metrics)
6. Named Docker volume persists Homebrew across container recreations
7. Port mappings in config map to Docker `-p` flags
8. Volume mounts in config map to Docker `-v` flags
9. Web UI hides WM/desktop options when backend is docker or podman
10. Web UI shows port mapping and volume mount editors for container backends
11. `go build ./...` and `go test ./internal/vmctl/...` pass
