# Agent VM Spec

Updated: 2026-06-15

## 1. Goal

Provide a single-command, reproducible development environment using Apple's native Container framework on macOS 26+.

- Base image: `Ubuntu 22.04` (arm64)
- Runtime: Apple Container CLI (`container`) with `--virtualization` (lightweight VM per container)
- Default user: `vm` (passwordless sudo), shell: `/usr/bin/zsh`
- Default resources: 6 CPU / 6 GiB RAM
- Single Go binary, flat package structure (no sub-packages)
- CLI: `agent-vm build && agent-vm start`
- Web portal: `agent-vm web` (OpenCode web + ttyd terminal)

## 2. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│  Host (macOS 26+)                                        │
│                                                          │
│  agent-vm web (port 8080)                                │
│  ┌────────────────────────────────────────────────────┐  │
│  │  Portal page (/, lists containers)                  │  │
│  │  API (/api/containers, /api/containers/<n>/start-*) │  │
│  │  Reverse proxy (subdomain-based routing)            │  │
│  └──────────────┬───────────────┬─────────────────────┘  │
│                 │               │                         │
│     <name>.localhost     <name>-term.localhost            │
│         → port 4096          → port 8082                  │
│                 │               │                         │
│          ┌──────┴───────┐────────┴───────┐                │
│          │  socat tunnel │  socat tunnel  │                │
│          │  (per HTTP    │  (per HTTP     │                │
│          │   connection) │   connection)  │                │
│          └──────┬───────┴────────┬───────┘                │
│                 │               │                         │
│  ═══════════════╪═══════════════╪══════════════════════   │
│  Container "dev"│               │                         │
│  ┌──────────────┴───────────────┴─────────────────────┐  │
│  │  OpenCode web (:4096)    ttyd (:8082)              │  │
│  │  zsh, Go, Node, Rust, Python, ...                  │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
```

### 2.1 No Host Ports

Containers are started with **no published ports**. The host web portal accesses
container-internal services through a **socat tunnel** — each HTTP/WebSocket
connection spawns `container exec -i <name> socat - TCP:127.0.0.1:<port>`,
giving the host process a bidirectional pipe directly into the container.

This means:
- A single host port (default 8080) serves all containers
- No port allocation or collision management
- No `-p` flag needed on `container run`

### 2.2 Subdomain Routing

The portal routes based on the `Host` header:

| Subdomain | Target | Port |
|---|---|---|
| `<name>.localhost:8080` | OpenCode web | 4096 |
| `<name>-term.localhost:8080` | ttyd terminal | 8082 |

`*.localhost` resolves to `127.0.0.1` on macOS/Linux, so no DNS setup needed.

### 2.3 Auto-Start

When a request arrives for a container service that isn't running yet, the
portal starts it automatically:

1. Check if the port is listening inside the container (`/dev/tcp` probe)
2. If not, run `container exec -u vm <name> bash -lc 'nohup <cmd> &'` using **absolute binary paths** (the non-interactive `bash -lc` does not source `~/.zshrc`, so PATH-based lookup is unreliable) and any required env vars (e.g. `BROWSER=/bin/true` for opencode web — see §5)
3. Poll until the port responds (up to 15 seconds)
4. If still not ready, read `/tmp/<label>.log` from inside the container and include its contents in the error response — so the actual failure reason (missing dep, crash, etc.) is surfaced instead of an opaque timeout
5. Proxy the original request

## 3. Container Lifecycle

### 3.1 Build

`agent-vm build` writes the embedded Dockerfile to a temp directory and runs
`container build -t kata-dev <tmpdir>`.

### 3.2 Start / Attach

`agent-vm start` merges start + exec:
1. If container is already running → skip creation, exec directly
2. If not → create with `container run -d --virtualization`, then exec

The workspace host path is persisted in state. On subsequent `start` calls
without `-w`, the stored path is reused. The container working directory is
resolved relative to the current host directory — if you're in a subfolder of
the workspace, the shell opens at the matching subfolder inside the container.

### 3.3 Managed Containers Only

Only containers started via `agent-vm` are tracked (identified by `.workspace`
state files). The `list` command and web portal only show managed containers.
Containers created directly via the `container` CLI are invisible to agent-vm.

## 4. Web Portal

### 4.1 Portal Page

Served at `http://localhost:8080/`. Lists all managed containers with:
- Running/stopped status
- OpenCode web status (running/idle)
- Terminal status (running/idle)
- **OpenCode** button — starts opencode web, opens `<name>.localhost`
- **Terminal** button — starts ttyd, opens `<name>-term.localhost`

Auto-refreshes every 3 seconds via `GET /api/containers`.

### 4.2 API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/containers` | JSON list of containers with status |
| POST | `/api/containers/<name>/start-web` | Ensure OpenCode web is running |
| POST | `/api/containers/<name>/start-terminal` | Ensure ttyd is running |

### 4.3 Reverse Proxy Implementation

Uses `httputil.NewSingleHostReverseProxy` from the standard library with a
custom `http.Transport`:

```go
proxy.Transport = &http.Transport{
    DialContext: func(ctx, network, addr) (net.Conn, error) {
        return dialContainer(ctx, name, port)  // spawns socat
    },
}
```

`dialContainer` creates a `pipeConn` (implements `net.Conn`) wrapping the
stdin/stdout pipes of `container exec -i <name> socat - TCP:127.0.0.1:<port>`.

The standard library's `ReverseProxy` handles:
- HTTP requests and responses (streaming, not buffered)
- WebSocket upgrades (bidirectional copy after 101)
- Binary data (OS pipes are 8-bit clean)
- Connection pooling via keep-alive (one socat per pooled connection)

## 5. OpenCode Web

### 5.1 Purpose

Provides the AI coding agent's browser interface inside the container.
Runs on port 4096.

### 5.2 Start Command

```
env BROWSER=/bin/true /home/vm/.opencode/bin/opencode web --port 4096 --hostname 0.0.0.0
```

- `BROWSER=/bin/true` — `opencode web` normally auto-opens the user's
  browser after starting the server. The container is headless (no
  browser, no `DISPLAY`), so this would hang. The `open` npm package
  honors `BROWSER` as the browser command; `/bin/true` is a no-op that
  returns immediately.
- Absolute path `/home/vm/.opencode/bin/opencode` — avoids relying on
  PATH being set up in the non-interactive login shell that
  `ensureService` uses.
- `--port 4096` — fixed port for the socat tunnel.
- `--hostname 0.0.0.0` — binds to all interfaces so the service is
  reachable via the socat tunnel to `127.0.0.1:4096`.

## 6. ttyd Web Terminal

### 6.1 Purpose

Provides a browser-based terminal for the container. Uses **ttyd** (single C
binary) with **xterm.js** frontend. Runs on port 8082 inside the container.

### 6.2 Mobile Support

ttyd's xterm.js provides on-screen special keys for touch devices:
- Ctrl, Alt, Shift modifiers
- Tab, Esc, arrow keys
- Customizable key bar

Bluetooth keyboards work natively without any special handling.

### 6.3 Start Command

```
/home/linuxbrew/.linuxbrew/bin/ttyd --port 8082 -W -t fontSize=14 zsh
```

- Absolute path `/home/linuxbrew/.linuxbrew/bin/ttyd` — no PATH dependency.
- `-W`: read-write (allow input)
- `-t fontSize=14`: terminal font size
- `zsh`: default shell

## 7. Image Contents

Built from a single self-contained `Dockerfile` (no external scripts):

| Layer | Contents |
|---|---|
| System | curl, wget, git, build-essential, socat, zsh, podman (rootless) |
| Browser deps | libnss3, libgbm1, libatk, libpango, fonts-liberation, ... |
| Go | System-wide (`/usr/local/go`), configurable version via `--build-arg` |
| fnm + Node.js | LTS via fnm, pnpm via corepack |
| Playwright | Global pnpm package + Chromium browser |
| Rust | Via rustup |
| Python | Via uv |
| opencode | AI coding agent |
| Homebrew | Linuxbrew for additional packages |
| ttyd | Web terminal (via Homebrew) |
| Shell profile | Dev tools PATH in `~/.dev-tools.sh`, sourced from both `~/.zshrc` (interactive zsh) and `~/.profile` (login bash) |

## 8. State

All state in `~/.config/agent-vm/`:

```
<name>.workspace    # host workspace path (for working directory resolution)
```

No port files, no PID files, no YAML config. Container state is derived from
the `container` CLI at runtime.

## 9. CLI Commands

| Command | Description |
|---|---|
| `build` | Build the kata-dev image |
| `start [name]` | Start container and attach; if running, just attach |
| `stop [name]` | Stop a running container |
| `restart [name]` | Stop, then start and attach |
| `list` | List managed containers (aliases: status, ls) |
| `web` | Start web portal |
| `destroy [name]` | Remove container and state |

## 10. Code Layout

```
main.go         entry point
commands.go     cobra subcommand definitions
container.go    container lifecycle (build, start, exec, stop, destroy, service mgmt)
web.go          web portal + socat tunnel reverse proxy
util.go         helpers (signal forwarding, CLI checks)
embed.go        embeds Dockerfile
Dockerfile      image definition (self-contained, no external scripts)
```

All files are in `package main` at the module root. No sub-packages.

## 11. Limitations

- **macOS 26+ only**: Requires Apple Container CLI (`container`)
- **socat required in image**: The tunnel mechanism depends on socat being installed in the container
- **One socat per connection**: Each HTTP/WebSocket connection spawns a `container exec` session. Amortized by keep-alive but adds overhead for many concurrent connections.
- **No persistence**: Container state is not persisted across host reboots. Containers must be re-started with `agent-vm start`.
