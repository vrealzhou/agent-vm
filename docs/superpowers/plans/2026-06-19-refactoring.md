# Agent-VM Refactoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Restructure agent-vm from monolithic `package main` into modular packages, migrate to urfave/cli v3, and add a placeholder-based credential management system.

**Architecture:** Three-phase incremental refactoring: (1) package restructuring with cobra, (2) CLI migration + secrets system, (3) integration and tests.

**Tech Stack:** Go 1.26, urfave/cli v3, gopkg.in/yaml.v3, Apple Container CLI

---

## Phase 1: Package Restructuring (keep cobra)

### Task 1: Create config package
- Move proxyConfig, secretsConfig, NetworkConfig types to `internal/config/`
- Move path helpers (stateDir, proxyConfigPath, etc.) to `internal/config/config.go`
- Export all types and functions (uppercase)
- Update all references

### Task 2: Create container package
- Move containerStart/Stop/Destroy/Exec/Status/Build to `internal/container/`
- Move web.go to `internal/container/web.go`
- Container package imports config package

### Task 3: Create proxy package
- Move proxy server, providers, access control to `internal/proxy/`
- Move kafka_proxy.go to `internal/proxy/kafka.go`
- Move CA generation to `internal/proxy/ca.go`

### Task 4: Create credential package
- Move credential.go (forwarding server + helpers) to `internal/credential/`

### Task 5: Create secrets package (stub)
- Create `internal/secrets/` with type definitions (for Phase 2)

### Task 6: Create network package
- Move network.go to `internal/network/`

### Task 7: Create cli package (keep cobra)
- Move commands.go to `internal/cli/`
- Update all command functions to use package-qualified calls

### Task 8: Update main.go and verify
- main.go imports `internal/cli` and calls cli.Run()
- Run all tests, verify everything works

## Phase 2: CLI Migration + Secrets System

### Task 9: Add urfave/cli v3 dependency
- `go get github.com/urfave/cli/v3`
- Create new command structure in `internal/cli/`

### Task 10: Migrate container commands
- Convert build/start/stop/restart/exec/destroy/list to urfave/cli v3

### Task 11: Add --profile flag and multi-config resolution
- Implement config resolution: project > profile > global
- Add `internal/config/resolve.go`

### Task 12: Implement secrets subcommand
- `secrets add/list/remove/show` commands
- Placeholder store (read/write secrets.yaml)
- Credential type definitions with field visibility

### Task 13: Remove cobra dependency
- `go mod tidy` to remove cobra

## Phase 3: Integration + Tests

### Task 14: proxy.yaml placeholder references
- Proxy providers reference placeholders by name
- Resolve placeholder → real credentials at proxy startup

### Task 15: Container env forwarding by type visibility
- Plaintext fields → env vars in container
- Secret fields → proxy-only

### Task 16: Update integration tests
- Test placeholder workflow end-to-end
- Test multi-config resolution

### Task 17: Final cleanup
- Remove old shell test scripts
- Update documentation
