# Agent VM

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A single-command, reproducible development environment using Apple's native Container framework on macOS 26+ — with built-in credential proxy, Kafka SASL interception, and placeholder-based secret management.

- Base image: `Debian 13` (arm64)
- Runtime: Apple Container with `--virtualization` (lightweight VM per container)
- Default user: `vm` (passwordless sudo), shell: `zsh`
- Resources: 6 CPU / 6 GiB RAM
- Pre-installed: Go, Node.js (fnm), pnpm, Playwright + Chromium, Rust, uv, opencode, Homebrew, ttyd, Podman (rootless), kafkacat, neovim
- Font: Maple Mono NF CN (Nerd Font icons + CJK glyphs)
- CLI: urfave/cli v3

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

## Credential Proxy

agent-vm includes a MITM proxy that transparently injects credentials into HTTPS requests — **real secrets never enter the container**.

### Secrets Management

```bash
# Add an AWS credential placeholder
agent-vm secrets add aws-prod --type aws-sigv4 \
    --field access_key=AKIAIOSFODNN7EXAMPLE \
    --field secret_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY \
    --field region=us-east-1 --field service=s3

# List placeholders
agent-vm secrets list

# Show (secrets masked by default)
agent-vm secrets show aws-prod
agent-vm secrets show aws-prod --reveal    # show actual values

# Remove
agent-vm secrets remove aws-prod
```

Each credential type predefines field visibility:
- **Plaintext fields** (e.g. `access_key`, `region`) → forwarded to container as env vars
- **Secret fields** (e.g. `secret_key`) → proxy-only, never exposed

### Proxy Configuration

`~/.config/agent-vm/proxy.yaml` (or per-project `.agent-vm/proxy.yaml`):

```yaml
# Reference placeholders by name
providers:
  "*.amazonaws.com":
    placeholder: aws-prod
  github.com:
    placeholder: github-token

# Or inline credentials
credentials:
  api.example.com:
    Authorization: "Bearer xxx"

# Whitelist: only allow these domains
whitelist:
  - "*.amazonaws.com"
  - github.com

# Kafka TCP proxy (non-HTTP credential replacement)
kafka_proxy:
  broker: real-kafka:9093
  sasl_username: real-user
  sasl_password: real-pass
  tls: true
```

### How It Works

```
Container (placeholder creds)
    │
    ▼ HTTPS_PROXY
MITM Proxy (host-side daemon)
    ├─ Generates CA cert, installed in container trust store
    ├─ Intercepts TLS for configured domains
    ├─ Decrypts → injects credentials → re-encrypts → forwards
    └─ Non-credential domains: transparent tunnel (no MITM)
```

### Built-in Credential Types

| Type | Use Case | Plaintext Fields | Secret Fields |
|---|---|---|---|
| `aws-sigv4` | AWS API calls | access_key, region, service | secret_key, session_token |
| `header` | REST API tokens | — | headers |
| `kafka-sasl` | Kafka SASL auth | broker, sasl_username, tls | sasl_password |

Custom types can be registered via `proxy.RegisterProvider()`.

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
agent-vm start -p 8080:80       # publish a port
agent-vm start --profile aws    # use a specific proxy profile
agent-vm start --no-proxy       # skip credential proxy
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

### Web Portal

```bash
agent-vm web                    # portal on http://localhost:8080
agent-vm web -p 9090            # custom port
```

## CLI Reference

| Command | Description |
|---|---|
| `build` | Build the `kata-dev` container image |
| `start [name]` | Start a container and attach |
| `stop [name]` | Stop a running container |
| `restart [name]` | Stop, then start and attach |
| `exec [name]` | Attach to a running container |
| `list` | List agent-vm managed containers (aliases: `status`, `ls`) |
| `destroy [name]` | Remove a container and its state |
| `web` | Start the web portal |
| `secrets add` | Add a credential placeholder |
| `secrets list` | List all placeholders |
| `secrets remove` | Remove a placeholder |
| `secrets show` | Show placeholder details |

### Flags

| Flag | Commands | Default | Description |
|---|---|---|---|
| `-n, --name` | start, stop, restart, destroy, exec | `dev` | Container name |
| `-c, --cpus` | start, restart | `6` | Number of CPUs |
| `-w, --workspace` | start, restart, exec | *(see note)* | Host folder to mount |
| `-d, --detach` | start | `false` | Start without attaching |
| `-g, --go-version` | build | *(from Dockerfile)* | Go version |
| `-p, --publish` | start, restart | — | Publish port (repeatable) |
| `--profile` | start, restart | — | Proxy config profile |
| `--no-proxy` | start, restart | `false` | Skip credential proxy |
| `-p, --port` | web | `8080` | Portal port |
| `-t, --type` | secrets add | — | Credential type |
| `-f, --field` | secrets add | — | Field as key=value (repeatable) |
| `--reveal` | secrets show | `false` | Show secret values |

## Configuration Files

All configs in YAML, stored in `~/.config/agent-vm/`:

| File | Scope | Purpose |
|---|---|---|
| `proxy.yaml` | Project or global | MITM proxy rules, providers, whitelist/blacklist, Kafka proxy |
| `secrets.yaml` | Global only | Credential placeholders (real secrets) |
| `network.yaml` | Global | Container network settings (DNS, ports, MTU) |
| `.agent-vm/proxy.yaml` | Per-project | Project-specific proxy config (overrides global) |
| `profiles/<name>.yaml` | Per-profile | Named proxy profiles (selected via `--profile`) |

### Config Resolution Priority

1. Project: `./.agent-vm/proxy.yaml`
2. Profile: `~/.config/agent-vm/profiles/<name>.yaml` (via `--profile`)
3. Global: `~/.config/agent-vm/proxy.yaml`

## What's Inside the Image

- **Languages:** Go (system-wide), Node.js LTS (via fnm), Rust, Python (via uv)
- **Package managers:** pnpm, Homebrew (Linuxbrew), Cargo
- **Browser automation:** Playwright + Chromium (with all required system libraries)
- **Containers:** Podman (rootless, fully isolated via fuse-overlayfs)
- **Web terminal:** ttyd (via Homebrew) — serves xterm.js in the browser
- **AI coding:** opencode
- **Kafka client:** kafkacat
- **Font:** Maple Mono NF CN (Nerd Font icons + CJK glyphs, monospace)
- **Shell:** zsh with dev tools PATH configured in `~/.dev-tools.sh`
- **Utilities:** socat, neovim, htop, git, curl, wget, build-essential
- **Locale:** en_US.UTF-8 with CJK font support

## Code Layout

```
main.go                       entry point
embed.go                      embeds Dockerfile
Dockerfile                    self-contained image (Debian 13 + zsh)
integration_test.go           E2E tests (build tag: integration)

internal/
  config/                     config types, paths, multi-level resolution
    config.go                 StateDir, workspace state
    proxy.go                  ProxyConfig, ProviderEntry
    secrets.go                SecretsConfig, SecretEntry
    network.go                NetworkConfig
    kafka.go                  KafkaProxyConfig
    resolve.go                project > profile > global resolution
  container/                  container lifecycle
    container.go              Start/Stop/Destroy/Exec/Status/Build
    web.go                    web portal + socat tunnel proxy
    util.go                   runCommand, runWithSignals, etc.
  proxy/                      MITM proxy + credential providers
    server.go                 MITM HTTP proxy server, TLS interception
    daemon.go                 proxy daemon lifecycle
    ca.go                     CA certificate generation
    access.go                 whitelist/blacklist access control
    providers.go              CredentialProvider interface + built-in types
    kafka.go                  Kafka TCP SASL proxy
    init.go                   container init script generation
  credential/                 credential forwarding (non-HTTP)
    credential.go             TCP server, env forwarding, git helper
  secrets/                    placeholder management
    store.go                  Placeholder store (secrets.yaml)
    types.go                  credential type definitions (field visibility)
  network/                    network config
    network.go                NetworkConfig → container run args
  cli/                        CLI command definitions
    commands.go               urfave/cli v3 commands
```

See [docs/specs.md](docs/specs.md) for the full architecture specification.
