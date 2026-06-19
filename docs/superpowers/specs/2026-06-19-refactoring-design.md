# Agent-VM Refactoring Design

Date: 2026-06-19

## Overview

Refactor agent-vm from a monolithic `package main` into a modular package
structure. Add a placeholder-based credential management system with a
`secrets` subcommand. Migrate CLI framework from cobra to urfave/cli v3.

## Decisions

- **proxy.yaml**: per-project (`.agent-vm/proxy.yaml`) + `--profile` override
  + global fallback (`~/.config/agent-vm/proxy.yaml`)
- **secrets.yaml**: global only (`~/.config/agent-vm/secrets.yaml`)
- **Field visibility**: predefined per credential type (not per-field flag)
- **CLI framework**: urfave/cli v3
- **Implementation**: 3 phases — packages, CLI+secrets, integration

## Package Structure

```
cmd/agent-vm/main.go              # entry point

internal/
  config/      # config types, paths, multi-level resolution
  container/   # container lifecycle (start/stop/exec/build/web)
  proxy/       # MITM proxy + credential providers + Kafka TCP proxy
  credential/  # credential forwarding (non-HTTP protocols)
  secrets/     # placeholder store + credential type definitions
  network/     # network config → container run args
  cli/         # CLI command definitions (urfave/cli v3)
```

## Config Resolution

Priority (high → low):
1. CLI flags (`--profile`)
2. Project: `./.agent-vm/proxy.yaml`
3. Profile: `~/.config/agent-vm/profiles/<name>.yaml`
4. Global: `~/.config/agent-vm/proxy.yaml`

secrets.yaml is always global.

## Credential Type System

Each type predefines field visibility:
- `aws-sigv4`: access_key(plaintext), secret_key(secret), region(plaintext), service(plaintext)
- `header`: headers(secret)
- `kafka-sasl`: broker(plaintext), sasl_username(plaintext), sasl_password(secret), tls(plaintext)

Plaintext fields are forwarded to container as env vars.
Secret fields are only used by the proxy for signing/injection.

## Placeholder Workflow

1. `agent-vm secrets add aws-prod --type aws-sigv4 --field access_key=... --field secret_key=...`
2. Stores in global secrets.yaml
3. proxy.yaml references: `placeholder: aws-prod`
4. Container start: plaintext fields → env vars; secret fields → proxy only
5. Proxy resolves placeholder → real credentials → signs/injects

## CLI Commands

```
agent-vm build [--go-version X]
agent-vm start [name] [-w dir] [-d] [--profile name] [-p host:ctr] [--no-proxy]
agent-vm stop [name]
agent-vm restart [name]
agent-vm exec [name] [-w dir]
agent-vm destroy [name]
agent-vm list

agent-vm secrets add <name> --type <type> --field key=value [--field ...]
agent-vm secrets list
agent-vm secrets remove <name>
agent-vm secrets show <name> [--reveal]

agent-vm web [--port 8080]
```

## Implementation Phases

### Phase 1: Package restructuring (keep cobra)
- Move code from `package main` to `internal/{config,container,proxy,...}`
- Keep all functionality working
- All existing tests pass

### Phase 2: CLI migration + secrets system
- Replace cobra with urfave/cli v3
- Add `--profile` flag and multi-config resolution
- Implement `secrets` subcommand (add/list/remove/show)
- Implement placeholder store and credential type system

### Phase 3: Integration + tests
- proxy.yaml placeholder references
- Container env forwarding based on type visibility
- Integration tests for placeholder workflow
