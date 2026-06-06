# Sub-Project 1: X11 + LXQt/xfce Guest Migration

Date: 2025-06-06

## Goal

Replace Wayland/sway inside the guest VM with X11-based LXQt and xfce desktop environments. Keep vfkit as the VM runner (UTM migration is a separate sub-project).

## Non-Goals

- UTM migration (Sub-project 2)
- SPICE clipboard wiring (Sub-project 2; only the guest-side `spice-vdagent` package is installed here)

## Changes

### 1. Config & Validation

**`config.go`** — validWMs: `{"lxqt": true, "xfce": true}`. Remove `sway`.
**`yaml_config.go`** — default `window_manager`: `"xfce"`.

### 2. Web UI

**`app.js`** — Bootstrap modal desktop dropdown: `["lxqt", "xfce"]`.
**`web_handlers.go`** — Bootstrap request struct: no schema change (field name stays `windowManager`).

### 3. Package Changes

Remove from both build and bootstrap:

- `sway`, `foot`, `wl-clipboard`, `wofi`, `mako`, `grim`, `slurp`
- `xdg-desktop-portal-wlr`, `seatd`

Add:

- `lxqt` (metapackage: lxqt-session, lxqt-panel, openbox, pcmanfm-qt, etc.)
- `spice-vdagent` (guest agent for UTM clipboard, installed but not wired yet)

Keep: `ghostty`, `xfce4`, `mesa`, `mesa-dri`, `chromium`, `fcitx5` stack, fonts.

### 4. Build Script (`build_vfkit.go`)

In `buildVMScript()` template:
- Update xbps install commands with new package lists
- Rewrite `vmctl-session` wrapper: LXQt branch uses `startlxqt`, xfce branch uses `startxfce4`
- Remove sway config.d writes (10-vmctl.conf, 20-vmctl-bar.conf)
- Remove `vmctl-swaybar-status` wrapper
- Remove `seatd` from service enable list

### 5. Bootstrap Script (`bootstrap_script.go`)

In the `bootstrapTemplate`:
- Remove `write_swaybar_status()` function
- Remove `write_window_manager_config()` function and its call
- Rewrite `write_session_wrapper()`: LXQt uses `startlxqt`, no Wayland-specific env
- Remove Wayland env vars (`WLR_RENDERER`, `WLR_NO_HARDWARE_CURSORS`)
- Remove Wayland socket detection loops from session wrapper
- Change fish/zsh autostart: check `DISPLAY` instead of `WAYLAND_DISPLAY`
- Also fix: remove dangling `install_rust` and `install_starship` calls, remove `write_starship_config()`, remove starship refs from fish/zsh configs, remove `.cargo`/`.rustup` from `fix_ownership()`, update final log message

### 6. Clipboard (`vm.go`)

- Replace `waylandClipboardShell()` with `x11ClipboardShell()` using `xclip`/`xsel` over SSH
- `ClipboardIn`: use `xclip -selection clipboard` instead of `wl-copy`
- `ClipboardOut`: use `xclip -selection clipboard -o` instead of `wl-paste --no-newline`

### 7. Guest Config Repair (`vm.go`, `build_vfkit.go`)

- `fixGuestConfig()`: remove `WAYLAND_DISPLAY` check from autostart scripts
- `injectGuestConfig()`: remove `WAYLAND_DISPLAY` check from autostart scripts
- Both: change to check `DISPLAY` only

### 8. Docs

- `specs.md` Section 11.2: remove wayland compositors, list LXQt, xfce
- `specs.md` Section 11.3: remove wayland-only tools (wl-clipboard, wofi, etc.)
- `specs.md` Section 1: update desktop description

## Verification

1. `go build ./...` compiles cleanly
2. `go test ./internal/vmctl/...` passes (config validation tests updated)
3. Generated bootstrap script contains no Wayland or sway references
4. Generated build script template contains LXQt + xfce package lists
5. Web UI bootstrap modal shows LXQt and xfce as desktop options
