# Docker Sandbox & Podman libkrun Backend Design

Date: 2026-06-06

## Goal

Add Docker sandbox (`sbx`) and Podman (libkrun driver) as alternative backends to UTM for running lightweight, headless Linux development environments. These backends use user-defined environment profiles (YAML) to generate container images with no GUI, no SSH daemon — all operations go through `sbx exec` / `podman exec`.

## Decisions

| Decision | Choice |
|---|---|
| Docker backend | `sbx` CLI (Docker Sandboxes) — microVM isolation |
| Podman backend | `podman` CLI with libkrun VM driver |
| Container operations | `sbx exec` / `podman exec` only — no SSH inside container |
| Environment definition | User-defined YAML profiles, committed to git, shared across teams |
| Image generation | Dockerfile generated from profile YAML at build time |
| Rootfs security | `--read-only` + tmpfs for /tmp, /run, /var/tmp (podman) |
| Persistence | Named volumes for `/home/vm` and `/home/linuxbrew/.linuxbrew` |
| File sharing | `-v` bind mounts at container start |
| Port forwarding | `-p` port mapping (podman) / `sbx ports --publish` (sbx) |
| Multi-container | One config = one container. Multiple projects share environment profiles |
| Runtime package installs | Via Homebrew (persisted) or language package managers (→ user home volume) |
| Architecture approach | Backend interface + dispatch |

## Environment Profiles

Environments are **user-defined YAML files** that describe what goes into a container image. They live in `~/.config/agent-vm/profiles/` and can be committed to git for team sharing.

### Profile YAML schema

```yaml
# ~/.config/agent-vm/profiles/go.yaml
name: go
description: "Go development environment"

base: alpine:latest

system_packages:
  - go
  - gopls
  - delve

brew_packages:
  - golangci-lint
  - goreleaser

post_install: |
  go install golang.org/x/tools/gopls@latest
  go install github.com/go-delve/delve/cmd/dlv@latest

env:
  GOPATH: /home/vm/go
  PATH: "$PATH:/home/vm/go/bin"
```

```yaml
# ~/.config/agent-vm/profiles/php-symfony.yaml
name: php-symfony
description: "PHP + Symfony development environment"

base: alpine:latest

system_packages:
  - php83
  - php83-cli
  - php83-json
  - php83-openssl
  - php83-pdo
  - php83-mbstring
  - php83-xml
  - php83-curl
  - composer

brew_packages: []

post_install: |
  curl -1sLf 'https://dl.cloudsmith.io/public/symfony/stable/setup.alpine.sh' | sudo sh
  sudo apk add symfony-cli

env:
  PATH: "$PATH:/home/vm/.composer/vendor/bin"
```

```yaml
# ~/.config/agent-vm/profiles/node.yaml
name: node
description: "Node.js development environment"

base: alpine:latest

system_packages: []

brew_packages:
  - pnpm
  - yarn

post_install: |
  # fnm already installed by base layer; install latest LTS
  eval "$(fnm env)"
  fnm install --lts
  fnm default lts-latest

env: {}
```

### Profile schema definition

```yaml
name: string           # required — profile name, used as image tag prefix
description: string    # optional — human-readable description
base: string           # required — base image (default: alpine:latest)
system_packages: []    # optional — apk packages to install
brew_packages: []      # optional — Homebrew packages to install
post_install: string   # optional — shell script to run after package installation
env: map               # optional — environment variables to set
```

### Profile resolution order

When `vmctl.yaml` references `environment: go`:

1. `~/.config/agent-vm/profiles/go.yaml` (user local)
2. `./profiles/go.yaml` (project-local, committed to git)
3. Error: profile not found

This allows teams to:
- Share profiles via git (commit `profiles/` to project repo)
- Override team defaults with local customizations in `~/.config/agent-vm/profiles/`

### Dockerfile generation from profile

`agent-vm build-image --profile go` generates a Dockerfile from the profile and the base config (shell, editor, timezone, git identity from `vmctl.yaml`):

```dockerfile
FROM alpine:latest

# Base system packages (always present)
RUN apk add --no-cache bash git curl wget sudo ca-certificates tzdata \
    build-base <shell-pkg> <editor-pkg> docker-cli

# User: non-root, no root login
RUN adduser -D -s /bin/<shell> vm && \
    echo "vm:ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vm && \
    passwd -l root

# Timezone
RUN cp /usr/share/zoneinfo/<timezone> /etc/localtime && \
    echo "<timezone>" > /etc/timezone

# Homebrew
RUN adduser -D -s /bin/bash linuxbrew && \
    su - linuxbrew -c '/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"' && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /home/vm/.profile

# fnm (for all environments — Node.js is universal)
USER vm
RUN curl -fsSL https://fnm.vercel.app/install | bash

# Profile: system_packages
USER root
RUN apk add --no-cache <system_packages from profile>

# Profile: brew_packages
RUN su - linuxbrew -c 'brew install <brew_packages from profile>'

# Profile: post_install script
USER vm
RUN <post_install from profile>

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
RUN git config --global core.editor <editor> && \
    git config --global init.defaultBranch main && \
    git config --global pull.rebase false && \
    git config --global push.autoSetupRemote true

# Profile: env vars
ENV <env from profile>

# Default directories
USER vm
RUN mkdir -p ~/repos ~/projects

# Entrypoint
COPY entrypoint.sh /entrypoint.sh
USER root
ENTRYPOINT ["/entrypoint.sh"]
CMD ["sleep", "infinity"]
```

Entrypoint (`entrypoint.sh`):
```bash
#!/bin/sh
exec "$@"
```

### Sharing profiles via git

Teams commit `profiles/` to their project or organization repo:

```
my-org/dev-envs/
├── profiles/
│   ├── go.yaml
│   ├── php-symfony.yaml
│   ├── node.yaml
│   └── python.yaml
├── README.md
```

Each developer clones this repo and symlinks or copies profiles:
```bash
git clone git@github.com:my-org/dev-envs.git ~/.config/agent-vm/profiles
# or per-project:
ln -s /path/to/project/profiles ./profiles
```

### Multiple projects sharing one environment

```yaml
# ~/projects/service-a/vmctl.yaml
backend: podman
environment: go
ports:
  - host: 3001
    guest: 3000

# ~/projects/service-b/vmctl.yaml
backend: podman
environment: go
ports:
  - host: 3002
    guest: 3000

# ~/projects/api/vmctl.yaml
backend: podman
environment: php-symfony
ports:
  - host: 8000
    guest: 8000
```

All three containers use the same built image (`agent-vm-go` or `agent-vm-php-symfony`). Each has its own:
- Container name (derived from `vm.name`)
- Named volumes (`<name>_home`, `<name>_brew`)
- Port mappings
- Bind mounts

Image is built once, reused by all containers referencing the same environment.

### Build command

```bash
agent-vm build-image --profile go
# Generates Dockerfile, builds image tagged as agent-vm-go
# Uses podman or docker depending on backend in vmctl.yaml

# Build all profiles referenced in current config
agent-vm build-image --all

# Build from a specific profile file
agent-vm build-image --file /path/to/my-profile.yaml
```

Image naming: `agent-vm-<profile.name>`.

Users can also use a pre-built or third-party image directly:
```yaml
backend: podman
image: my-registry/my-dev-env:latest   # skip profile, use image directly
```

When `image:` is set, profile is ignored. When `environment:` is set, image is derived from profile name.

## Config Changes

### New YAML fields in `vmctl.yaml`

```yaml
backend: utm                    # "utm" | "sbx" | "podman"
environment: go                 # profile name (only for sbx/podman, mutually exclusive with image:)
# image: my-custom-image:latest # or use a pre-built image directly

# Only used when backend is sbx or podman:
volumes:
  - name: projects
    host_path: /Users/me/projects
    mount: /home/vm/projects

ports:
  - host: 3000
    guest: 3000
```

Named volumes are auto-created: `<name>_home` → `/home/vm`, `<name>_brew` → `/home/linuxbrew/.linuxbrew`. Always present for container backends, not user-configurable.

### Config struct changes

New fields in `Config`:
- `Backend string` — `"utm"`, `"sbx"`, or `"podman"`
- `ContainerName string` — derived from `Name` (e.g., `agent-vm-void-dev`)
- `Environment string` — profile name
- `Image string` — resolved image name (derived from profile or set directly)
- `Volumes []VolumeMount` — user-defined bind mounts
- `PortMappings []PortMapping` — host:guest port pairs

New types:
```go
type VolumeMount struct {
    Name     string
    HostPath string // set for bind mounts
    Mount    string
}

type PortMapping struct {
    Host  int
    Guest int
}
```

Existing UTM-only fields remain but are only populated when `Backend == "utm"`.

### Validation

`validateConfig()` branches on `cfg.Backend`:
- **UTM**: validates WM (lxqt/xfce), network fields, checks `utmctl` binary
- **sbx**: checks `sbx` binary, validates profile or image exists
- **podman**: checks `podman` binary, validates profile or image exists, validates port mappings, validates libkrun driver

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
| `PodmanBackend` | `podman` | `podman run/exec/stop/rm`, libkrun, `--read-only` + security flags |

### UTMBackend

Wraps existing UTM/utmctl lifecycle. Moves current free functions into struct methods. No behavior change.

### SBXBackend

Uses `sbx` CLI (Docker Sandboxes):
- `sbx create --name <name> .` — create sandbox with workspace
- `sbx exec <name> <cmd>` — run commands inside
- `sbx stop <name>` — stop sandbox
- `sbx rm <name>` — remove sandbox
- `sbx ports <name> --publish host:guest` — port forwarding
- Persistence: sbx sandboxes persist state across stops; removed on `sbx rm`
- Image/Environment: sbx sandboxes have their own environment setup. Profile's `post_install` runs via `sbx exec` after creation.

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
- Container runs as user `vm` (`--user vm`)
- sudo available for specific operations but no root shell

### Layer 4: Persistent writable paths (named volumes only)
- `agent-vm-<name>_home` → `/home/vm` (user data, configs, projects, go/bin, .npm, etc.)
- `agent-vm-<name>_brew` → `/home/linuxbrew/.linuxbrew` (Homebrew packages)
- User-defined bind mounts for project files

### Runtime package installation
- No `apk` at runtime (rootfs is read-only)
- Homebrew (`brew install`) → persisted in brew volume
- Language package managers (`go install`, `npm i -g`, `pip install --user`) → go to user home volume
- These persist across container recreations

## Container Lifecycle

### SBXBackend.Start()

1. Check `sbx` binary exists
2. Check if sandbox already exists: `sbx ls`
3. If not exists: `sbx create --name <name> .`
4. If sandbox not running: `sbx run <name>` (starts sandbox)
5. If no bootstrap marker: run profile's `post_install` via `sbx exec`, then hook scripts, write marker
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
2. Resolve image name from `environment` profile or `image` field
3. Check if image exists: `podman image inspect <image>`
4. If not: return error (user must `build-image` first)
5. Check if container exists: `podman inspect <containerName>`
6. If container exists but stopped: `podman start <containerName>`
7. If no container:
   ```
   podman run -d --name <containerName> \
     --read-only --cap-drop=ALL \
     --security-opt no-new-privileges \
     --pids-limit 4096 \
     --tmpfs /tmp:rw,noexec,nosuid \
     --tmpfs /run:rw,noexec,nosuid \
     --tmpfs /var/tmp:rw,noexec,nosuid \
     -v <name>_home:/home/vm \
     -v <name>_brew:/home/linuxbrew/.linuxbrew \
     [-p host:guest ...] \
     [-v host_path:container_path ...] \
     <image>
   ```
8. If no bootstrap marker: run hook scripts via `podman exec`, write marker

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
| Environment | **hidden** | shown (dropdown: profiles from `profiles/` dir) |
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

```bash
# Build from a profile
agent-vm build-image --profile go
# Generates Dockerfile from profiles/go.yaml + base config, builds image agent-vm-go

# Build all profiles referenced in current vmctl.yaml
agent-vm build-image --all

# Build from a specific file
agent-vm build-image --file /path/to/my-profile.yaml

# Uses podman or docker depending on what's available
```

## Files Changed

| File | Change |
|---|---|
| `internal/vmctl/config.go` | Add `Backend`, `Environment`, `Image`, `ContainerName`, `Volumes`, `PortMappings`; backend-aware validation |
| `internal/vmctl/yaml_config.go` | Parse `backend:`, `environment:`, `image:`, `volumes:`, `ports:` |
| `internal/vmctl/profile.go` | **New** — `EnvironmentProfile` struct, YAML loading, profile resolution |
| `internal/vmctl/dockerfile.go` | **New** — Dockerfile generation from profile + config |
| `internal/vmctl/backend.go` | **New** — `Backend` interface, `NewBackend()` factory |
| `internal/vmctl/backend_utm.go` | **New** — `UTMBackend` (moves code from vm.go, inspect.go) |
| `internal/vmctl/backend_sbx.go` | **New** — `SBXBackend` (sbx CLI) |
| `internal/vmctl/backend_podman.go` | **New** — `PodmanBackend` (podman CLI + security) |
| `internal/vmctl/vm.go` | Trimmed — delegates to backend |
| `internal/vmctl/inspect.go` | Trimmed — delegates to backend |
| `internal/vmctl/cobra.go` | `build-image` command, backend-aware commands |
| `internal/vmctl/web_handlers.go` | Backend-aware handlers, profile listing |
| `web/static/app.js` | Conditional UI, backend selector, environment dropdown |
| `web/static/index.html` | Backend selector, port/volume inputs |
| `internal/vmctl/profile_test.go` | **New** — profile parsing tests |
| `internal/vmctl/dockerfile_test.go` | **New** — Dockerfile generation tests |

## Acceptance Criteria

1. `backend: utm` produces identical behavior to current system
2. `backend: sbx` creates/manages sandboxes via `sbx` CLI
3. `backend: podman` runs containers with `--read-only --cap-drop=ALL --security-opt no-new-privileges`
4. Podman verifies libkrun driver on first start
5. No SSH daemon inside containers
6. `sbx exec` / `podman exec` for all container operations
7. Named volumes persist `/home/vm` and `/home/linuxbrew/.linuxbrew` across recreations
8. Users define environments via YAML profiles in `profiles/` directory
9. Profiles are resolved from `./profiles/` (project-local, git-shared) or `~/.config/agent-vm/profiles/` (user-local)
10. `build-image --profile <name>` generates Dockerfile from profile and builds image
11. Multiple `vmctl.yaml` configs can reference the same environment — image built once, shared
12. Port mappings in config map to `-p` / `sbx ports`
13. Volume mounts in config map to `-v` bind mounts
14. Web UI hides WM/desktop when backend is sbx/podman
15. Web UI shows environment dropdown populated from `profiles/` directory
16. Root login disabled in container images
17. `go build ./...` and `go test ./internal/vmctl/...` pass
