# Agent VM Spec

Updated: 2026-06-22

## 1. Goal

Provide a single-command, reproducible development environment using Apple's native Container framework on macOS 26+ — with built-in credential proxy, Kafka SASL interception, and placeholder-based secret management.

- Base image: `Debian 13` (arm64)
- Runtime: Apple Container CLI (`container`) with `--virtualization` (lightweight VM per container)
- Default user: `vm` (passwordless sudo), shell: `/usr/bin/zsh`
- Default resources: 6 CPU / 6 GiB RAM
- CLI framework: urfave/cli v3
- Modular Go packages under `internal/`
- Credential proxy: MITM HTTPS interception + Kafka TCP SASL proxy
- Secret management: placeholder system with type-based field visibility

## 2. Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│  Host (macOS 26+)                                                │
│                                                                  │
│  agent-vm binary                                                 │
│  ├─ CLI (urfave/cli v3)                                          │
│  ├─ Web portal (:8080, socat tunnel reverse proxy)               │
│  │                                                               │
│  ├─ MITM Proxy daemon (per container)                            │
│  │  ├─ CA cert generation (ECDSA P-256)                          │
│  │  ├─ TLS interception for credential domains                   │
│  │  ├─ Provider chain: header / body / aws-sigv4 / custom        │
│  │  └─ Whitelist/blacklist access control                        │
│  │                                                               │
│  ├─ Credential server daemon (per container)                     │
│  │  ├─ Env var forwarding (secrets.yaml → container .dev-tools)  │
│  │  └─ Git/Docker credential helper protocol                     │
│  │                                                               │
│  └─ Kafka TCP proxy daemon (per container)                       │
│     ├─ Binary frame parsing (API key 36 = SASL_AUTHENTICATE)     │
│     └─ SASL/PLAIN credential replacement                         │
│                                                                  │
│  ══════════════════════════════════════════════════════════════════│
│  Apple Container VM (lightweight Linux VM)                       │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  socat bridges:                                             │  │
│  │    :18080 → host:proxy_port    (HTTPS proxy)                │  │
│  │    :18081 → host:cred_port     (credential server)          │  │
│  │    :18082 → host:kafka_port    (Kafka proxy)                │  │
│  │                                                             │  │
│  │  HTTP_PROXY/HTTPS_PROXY → 127.0.0.1:18080                   │  │
│  │  OpenCode web (:4096), ttyd (:8082)                         │  │
│  │  zsh, Go, Node, Rust, Python, kafkacat, Podman, ...         │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

### 2.1 No Host Ports for Services

Container services (OpenCode web, ttyd) are accessed through socat tunnels
spawned per HTTP connection. A single host port (8080) serves all containers
via subdomain-based routing.

For credential proxies, socat bridges on fixed in-container ports (18080-18082)
forward to host-side daemons via the auto-detected gateway IP.

### 2.2 Gateway Detection

The container init script detects the host gateway IP from `/proc/net/route`:

```bash
HEX=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route)
GW=$(printf "%d.%d.%d.%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2})
```

Typically resolves to `192.168.64.1` for Apple Container VMs.

## 3. Credential Proxy System

### 3.1 MITM HTTPS Proxy

For domains with configured credentials, the proxy performs TLS interception:

1. Client sends `CONNECT host:443` through the HTTP proxy
2. Proxy generates a leaf certificate signed by the proxy CA
3. TLS handshake with client (client trusts CA, installed in container)
4. Proxy decrypts HTTP requests, injects credentials, re-encrypts, forwards
5. For non-credential domains: transparent CONNECT tunnel (no interception)

The CA certificate is generated on first use (ECDSA P-256, 10-year validity),
stored in `~/.config/agent-vm/proxy-ca.crt` (PEM) and `proxy-ca.key` (DER).
At container startup, the CA cert is installed via `update-ca-certificates`.

### 3.2 Credential Providers

Pluggable provider interface:

```go
type CredentialProvider interface {
    Transform(req *http.Request) error
}
```

Built-in providers:
- **header** — inject HTTP headers (e.g., Authorization: Bearer xxx)
- **body** — modify JSON body fields (dot-path notation)
- **aws-sigv4** — full AWS SigV4 request signing (HMAC-SHA256)

Custom providers registered via `proxy.RegisterProvider(name, factory)`.

### 3.3 Placeholder System

Named credentials stored in global `secrets.yaml`:

```yaml
placeholders:
  aws-prod:
    type: aws-sigv4
    fields:
      access_key: AKIAIOSFODNN7EXAMPLE    # plaintext → enters container
      secret_key: wJalrXUtnFEMI/...        # secret → proxy only
      region: us-east-1
      service: s3
```

`proxy.yaml` references placeholders by name:

```yaml
providers:
  "*.amazonaws.com":
    placeholder: aws-prod
```

Field visibility is predefined per credential type. Plaintext fields are
forwarded to the container as environment variables. Secret fields are only
available to the proxy for signing/injection.

### 3.4 Kafka TCP SASL Proxy

For non-HTTP protocols, a separate TCP proxy intercepts Kafka binary frames:

1. Client connects to proxy via socat bridge (port 18082)
2. Proxy connects to real broker
3. For each client→broker frame:
   - API key 17 (SASL_HANDSHAKE) → transparent passthrough
   - API key 36 (SASL_AUTHENTICATE) → replace `\0username\0password` with real credentials, recompute frame length
   - All other frames → transparent passthrough
4. Broker→client direction: transparent `io.Copy`

Supports optional TLS to the real broker (SASL_SSL).

### 3.5 Credential Forwarding (non-proxy)

For tools that use credential helpers (git, docker) or environment variables:

- **Git**: `git-credential-agentvm` helper installed, queries credential server via socat
- **Env vars**: `secrets.yaml` `env:` section forwarded to container's `.dev-tools.sh`
- **Credential server**: TCP daemon on host, accessible via socat bridge (port 18081)

### 3.6 Access Control

- **Whitelist**: if set, only matching domains/URL-prefixes are allowed
- **Blacklist**: if no whitelist, matching domains are blocked
- Matching: exact domain, wildcard (`*.example.com`), URL prefix (`https://api.example.com/v1/`)

## 4. Container Lifecycle

### 4.1 Build

`agent-vm build` writes the embedded Dockerfile to a temp directory and runs
`container build -t kata-dev <tmpdir>`.

### 4.2 Start

`agent-vm start` performs:
1. Start MITM proxy daemon (if proxy.yaml exists)
2. Start credential server daemon (if secrets.yaml exists)
3. Start Kafka proxy daemon (if kafka_proxy configured)
4. Run `container run -d --virtualization` with init script
5. Init script: install CA cert, detect gateway, start socat bridges, write env vars, install helpers
6. Attach (unless `--detach`)

### 4.3 Config Resolution

Priority (high → low):
1. CLI flags (`--profile`)
2. Project: `./.agent-vm/proxy.yaml`
3. Profile: `~/.config/agent-vm/profiles/<name>.yaml`
4. Global: `~/.config/agent-vm/proxy.yaml`

`secrets.yaml` is always global.

## 5. Web Portal

Served at `http://localhost:8080/`. Lists managed containers with status,
provides browser-based OpenCode web and ttyd terminal access via socat
tunnel reverse proxy with subdomain routing.

| Subdomain | Target | Port |
|---|---|---|
| `<name>.localhost:8080` | OpenCode web | 4096 |
| `<name>-term.localhost:8080` | ttyd terminal | 8082 |

## 6. Image Contents

Built from a single self-contained `Dockerfile` (Debian 13):

| Layer | Contents |
|---|---|
| System | curl, wget, git, build-essential, socat, zsh, podman, neovim, htop, kafkacat |
| Browser deps | libnss3, libgbm1, libatk, libpango, fonts-liberation, ... |
| Locale | en_US.UTF-8, fonts-noto-cjk |
| Font | Maple Mono NF CN (Nerd Font + CJK, from GitHub releases) |
| Go | System-wide, configurable version via `--build-arg` |
| fnm + Node.js | LTS via fnm, pnpm via corepack |
| Playwright | Global pnpm package + Chromium browser |
| Rust | Via rustup |
| Python | Via uv |
| opencode | AI coding agent |
| Homebrew | Linuxbrew for additional packages |
| ttyd | Web terminal (via Homebrew) |
| Shell | zsh, dev tools PATH in `~/.dev-tools.sh`, sourced from `.zshrc` and `.profile` |

## 7. CLI Commands

| Command | Description |
|---|---|
| `build` | Build the kata-dev image |
| `start [name]` | Start container and attach |
| `stop [name]` | Stop a running container |
| `restart [name]` | Stop, then start and attach |
| `exec [name]` | Attach to a running container |
| `list` | List managed containers (aliases: status, ls) |
| `destroy [name]` | Remove container and state |
| `web` | Start web portal |
| `secrets add <name>` | Add a credential placeholder |
| `secrets list` | List all placeholders |
| `secrets remove <name>` | Remove a placeholder |
| `secrets show <name>` | Show placeholder details |

## 8. Package Structure

```
cmd/agent-vm/main.go              entry point (package main)

internal/
  config/      config types, YAML loading, multi-level path resolution
  container/   container lifecycle, web portal, utilities
  proxy/       MITM proxy server, credential providers, Kafka proxy, access control
  credential/  credential forwarding (TCP server, env vars, git helper)
  secrets/     placeholder store, credential type definitions
  network/     network config → container run args
  cli/         urfave/cli v3 command definitions
```

Each package has clear boundaries. Cross-package communication via exported
types and functions. Config types in `config/`, business logic in feature packages.

## 9. State

All state in `~/.config/agent-vm/`:

```
proxy.yaml            MITM proxy config (global)
secrets.yaml          credential placeholders (global)
network.yaml          network config (global)
proxy-ca.crt          proxy CA certificate (PEM)
proxy-ca.key          proxy CA private key (DER)
.agent-vm/proxy.yaml  project-level proxy config
profiles/<name>.yaml  named profiles
<name>.workspace      host workspace path
<name>.proxy.pid      proxy daemon PID
<name>.cred.pid       credential server PID
<name>.kafka.pid      kafka proxy PID
```

## 10. Testing

- **Unit tests** (`go test -short ./...`): access control, provider logic, Kafka frame parsing, config resolution
- **Integration tests** (`go test -tags integration .`): MITM header injection, AWS SigV4 signing, whitelist blocking, credential forwarding, git helper, Kafka SASL proxy, secrets workflow

## 11. Limitations

- **macOS 26+ only**: Requires Apple Container CLI
- **socat required**: All proxy/credential bridges depend on socat in the container
- **No GPU acceleration**: Desktop environments would run software-rendered only
- **HTTP/2 downgraded to 1.1**: MITM proxy forces HTTP/1.1 via NextProtos
- **Certificate pinning**: Apps with cert pinning reject the proxy's CA-signed certs
- **Rootless podman inside container**: `newuidmap` not permitted (Apple Container kernel limitation)
