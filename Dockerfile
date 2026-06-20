FROM debian:13

ARG GO_VERSION=1.23.4

ENV DEBIAN_FRONTEND=noninteractive
ENV SHELL=/usr/bin/zsh
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8
ENV LANGUAGE=en_US:en

# ── System packages + Playwright chromium deps + podman (rootless) + socat + zsh + neovim + htop ──
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl wget git unzip build-essential pkg-config sudo ca-certificates \
    libnss3 libnspr4 libatk1.0-0t64 libatk-bridge2.0-0t64 \
    libcups2t64 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 \
    libxfixes3 libxrandr2 libgbm1 libpango-1.0-0 libcairo2 \
    libasound2t64 libatspi2.0-0t64 fonts-liberation fontconfig \
    podman uidmap slirp4netns fuse-overlayfs \
    socat zsh neovim htop kafkacat \
    locales fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/* && \
    sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && locale-gen

# ── User ──
RUN useradd -m -s /usr/bin/zsh vm && \
    echo "vm ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers && \
    echo "vm:100000:65536" > /etc/subuid && \
    echo "vm:100000:65536" > /etc/subgid

# ── Go (system-wide) ──
RUN ARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" \
    | tar -C /usr/local -xz

# ── Maple Mono NF CN (Nerd Font icons + CJK glyphs, monospace) ──
RUN curl -fsSL -o /tmp/maple.zip \
    "https://github.com/subframe7536/maple-font/releases/download/v7.9/MapleMono-NF-CN.zip" && \
    mkdir -p /usr/share/fonts/truetype/maple-nf-cn && \
    unzip -o /tmp/maple.zip -d /usr/share/fonts/truetype/maple-nf-cn && \
    rm /tmp/maple.zip && \
    fc-cache -f

USER vm

# ── Initialize zsh completions before any tool appends compdef calls ──
RUN printf 'autoload -Uz compinit && compinit\n' > "$HOME/.zshrc"

# ── fnm + Node.js LTS ──
RUN export FNM_DIR="$HOME/.fnm" && \
    curl -fsSL https://fnm.vercel.app/install | bash -s -- --skip-shell --install-dir "$FNM_DIR" && \
    export PATH="$FNM_DIR:$PATH" && \
    eval "$(fnm env --shell bash)" && \
    fnm install --lts && \
    fnm default lts-latest && \
    fnm use default

# ── pnpm + Playwright ──
RUN export PATH="$HOME/.fnm:$PATH" && \
    eval "$(fnm env --shell bash)" && \
    corepack enable && \
    corepack prepare pnpm@latest --activate && \
    export PNPM_HOME="$HOME/.local/share/pnpm" && \
    export PATH="$PNPM_HOME:$PNPM_HOME/bin:$PATH" && \
    pnpm add -g playwright && \
    playwright install chromium

# ── uv (Python) ──
RUN curl -LsSf https://astral.sh/uv/install.sh | sh

# ── Rust ──
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y

# ── opencode ──
RUN curl -fsSL https://opencode.ai/install | bash

# ── Homebrew (Linuxbrew) ──
RUN NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# ── ttyd (web terminal) ──
RUN eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && brew install ttyd

# ── Podman config (rootless) ──
RUN mkdir -p "$HOME/.config/containers" && \
    printf '[storage]\ndriver = "fuse-overlayfs"\ngraphroot = "/home/vm/.local/share/containers/storage"\n' \
    > "$HOME/.config/containers/storage.conf"

# ── Dev tools env (sourced by both .zshrc and .profile) ──
# Written to a shared file so interactive zsh (.zshrc) and login bash
# (.profile) — used by `container exec ... bash -lc` for service startup —
# both see the same PATH (ttyd, opencode, fnm, cargo, go, ...).
RUN printf '%s\n' \
    '' \
    '# Dev tools PATH' \
    'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' \
    'export PNPM_HOME="$HOME/.local/share/pnpm"' \
    'export PATH="$HOME/.opencode/bin:$HOME/.fnm:$PNPM_HOME:$PNPM_HOME/bin:$HOME/.local/bin:$HOME/.cargo/bin:/usr/local/go/bin:$HOME/go/bin:$PATH"' \
    'if [ -n "$ZSH_VERSION" ]; then eval "$(fnm env --shell zsh)"; elif [ -n "$BASH_VERSION" ]; then eval "$(fnm env --shell bash)"; fi' \
    '[ -f "$HOME/.cargo/env" ] && source "$HOME/.cargo/env"' \
    > "$HOME/.dev-tools.sh" && \
    printf '\n[ -f "$HOME/.dev-tools.sh" ] && . "$HOME/.dev-tools.sh"\n' >> "$HOME/.zshrc" && \
    printf '\n[ -f "$HOME/.dev-tools.sh" ] && . "$HOME/.dev-tools.sh"\n' >> "$HOME/.profile"

WORKDIR /home/vm

CMD ["sleep", "infinity"]
