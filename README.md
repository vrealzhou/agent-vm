# Agent VM

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A single-command, reproducible development environment using Apple's native Container framework on macOS 26+.

- Base image: `Ubuntu 22.04` (arm64)
- Runtime: Apple Container with `--virtualization` (lightweight VM per container)
- Default user: `vm` (passwordless sudo), shell: `zsh`
- Resources: 6 CPU / 6 GiB RAM
- Pre-installed: Go, Node.js (fnm), pnpm, Playwright + Chromium, Rust, uv, opencode, Homebrew, ttyd, Podman (rootless)

## Host Requirements

**Platform:** macOS 26+ with Apple Container CLI.

```bash
command -v container
```

## Install

```bash
go install github.com/vrealzhou/agent-vm@latest
```

## Quick Start

```bash
agent-vm build                    # build the kata-dev image
agent-vm start -w ~/projects      # start container + attach
agent-vm web                      # open http://localhost:8080
```

If the container is already running, `start` skips creation and attaches directly — dropping you into the VM directory that matches your current host folder.

## Usage

### Build the image

```bash
agent-vm build                  # uses Dockerfile's default Go version
agent-vm build -g 1.24.0        # specify a Go version
```

### Start / Attach

```bash
agent-vm start                  # start (or attach to) the default container "dev"
agent-vm start -w ~/projects    # start with a specific workspace mount
agent-vm start myapp            # start a named container
agent-vm start -d               # start without attaching (detached)
agent-vm start -c 8             # allocate 8 CPUs
```

When you run `start` inside a subfolder of the workspace, the container shell opens at the corresponding subfolder under `/home/vm/workspace`:

```
host:       ~/projects/myapp/src
container:  /home/vm/workspace/myapp/src
```

### Stop / Restart / Destroy

```bash
agent-vm stop                   # stop the default container
agent-vm restart                # stop + start + attach
agent-vm destroy                # remove the container and its state
```

All commands accept an optional `[name]` argument or `-n` flag to target a specific container.

### List containers

```bash
agent-vm list                   # alias: status, ls
```

Only shows containers started via `agent-vm`. Containers created directly with the `container` CLI are not included.

### Web Portal

```bash
agent-vm web                    # portal on http://localhost:8080
agent-vm web -p 9090            # custom port
```

The portal provides browser-based access to every container through a single host port — **no host port publishing needed**. Each running container shows two buttons:

- **OpenCode** — opens the OpenCode web interface (AI coding agent)
- **Terminal** — opens a full web terminal (ttyd + xterm.js) with mobile keyboard support

**Access URLs** (subdomain-based routing via `*.localhost`):

| URL | Service | Internal port |
|---|---|---|
| `http://<name>.localhost:8080` | OpenCode web | 4096 |
| `http://<name>-term.localhost:8080` | ttyd terminal | 8082 |

**How it works — socat tunnel (no host ports):**

Instead of publishing ports, the portal creates a bidirectional tunnel per HTTP connection:

```
browser → agent-vm web (8080) → container exec -i <name> socat - TCP:127.0.0.1:<port>
```

Each connection spawns a `container exec` + `socat` process that pipes traffic directly into the container. The standard library's `httputil.ReverseProxy` handles HTTP, WebSocket, and binary data transparently.

**Auto-start:** If a service (OpenCode web or ttyd) isn't running inside a container, the portal starts it automatically on first access.

**Mobile terminal:** ttyd's xterm.js provides on-screen Ctrl, Tab, Esc, arrow keys — works on iPad virtual keyboards. Bluetooth keyboards work natively.

## CLI Reference

| Command | Description |
|---|---|
| `build` | Build the `kata-dev` container image |
| `start [name]` | Start a container and attach; if already running, just attach |
| `stop [name]` | Stop a running container |
| `restart [name]` | Stop, then start and attach |
| `list` | List agent-vm managed containers (aliases: `status`, `ls`) |
| `web` | Start the web portal (OpenCode web + terminal) |
| `destroy [name]` | Remove a container and its state |

### Flags

| Flag | Commands | Default | Description |
|---|---|---|---|
| `-n, --name` | start, stop, restart, destroy | `dev` | Container name |
| `-c, --cpus` | start, restart | `6` | Number of CPUs |
| `-w, --workspace` | start, restart | *(see note)* | Host folder to mount at `/home/vm/workspace` |
| `-d, --detach` | start | `false` | Start without attaching |
| `-g, --go-version` | build | *(from Dockerfile)* | Go version to install in the image |
| `-p, --port` | web | `8080` | Portal port |

> **Workspace default:** when omitted, `start` reuses the workspace from the previous start. On first start it defaults to the current directory.

## State

Container state is persisted in `~/.config/agent-vm/`:

```
<name>.workspace    # host workspace path (for working directory resolution)
```

Only containers started via `agent-vm` are tracked. The `list` command and web portal scan this directory — containers created directly with the `container` CLI are invisible.

## What's Inside the Image

The image (`kata-dev`) is built from a single self-contained `Dockerfile`:

- **Languages:** Go (system-wide), Node.js LTS (via fnm), Rust, Python (via uv)
- **Package managers:** pnpm, Homebrew (Linuxbrew), Cargo
- **Browser automation:** Playwright + Chromium (with all required system libraries)
- **Containers:** Podman (rootless, fully isolated via fuse-overlayfs)
- **Web terminal:** ttyd (via Homebrew) — serves xterm.js in the browser
- **AI coding:** opencode
- **Shell:** zsh with dev tools PATH configured in `~/.zshrc`
- **Utilities:** socat (for web portal tunneling), git, curl, wget, build-essential

## Code Layout

```
main.go         entry point
commands.go     cobra subcommand definitions
container.go    container lifecycle + service management
web.go          web portal + socat tunnel reverse proxy
util.go         helpers (signal forwarding, CLI checks)
embed.go        embeds Dockerfile into the binary
Dockerfile      self-contained image definition (Ubuntu 22.04 + zsh)
```

All files are in `package main` at the module root — no sub-packages. See [docs/specs.md](docs/specs.md) for the full architecture specification.
