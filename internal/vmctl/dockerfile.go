package vmctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func GenerateDockerfile(cfg Config, profile EnvironmentProfile) (string, error) {
	shellPkg := shellPackage(cfg.DefaultShell)
	editorPkg := editorPackage(cfg.DefaultEditor)
	shellBin := shellBinary(cfg.DefaultShell)
	editorCmd := editorCommand(cfg.DefaultEditor)

	var b strings.Builder

	fmt.Fprintf(&b, "FROM %s\n\n", profile.Base)

	fmt.Fprintf(&b, "RUN apk add --no-cache bash git curl wget sudo ca-certificates tzdata build-base %s %s docker-cli\n\n", shellPkg, editorPkg)

	fmt.Fprintf(&b, "RUN adduser -D -s %s vm && \\\n", shellBin)
	fmt.Fprintf(&b, "    echo \"vm:ALL=(ALL) NOPASSWD:ALL\" >> /etc/sudoers.d/vm && \\\n")
	fmt.Fprintf(&b, "    passwd -l root\n\n")

	if cfg.Timezone != "" {
		fmt.Fprintf(&b, "RUN cp /usr/share/zoneinfo/%s /etc/localtime && \\\n", cfg.Timezone)
		fmt.Fprintf(&b, "    echo \"%s\" > /etc/timezone\n\n", cfg.Timezone)
	}

	fmt.Fprintf(&b, "RUN adduser -D -s /bin/bash linuxbrew && \\\n")
	fmt.Fprintf(&b, "    su - linuxbrew -c '/bin/bash -c \"$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\"' && \\\n")
	fmt.Fprintf(&b, "    echo 'eval \"$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)\"' >> /home/vm/.profile\n\n")

	fmt.Fprintf(&b, "USER vm\n")
	fmt.Fprintf(&b, "RUN curl -fsSL https://fnm.vercel.app/install | bash\n\n")

	if len(profile.SystemPackages) > 0 {
		fmt.Fprintf(&b, "USER root\n")
		fmt.Fprintf(&b, "RUN apk add --no-cache %s\n\n", strings.Join(profile.SystemPackages, " "))
	}

	if len(profile.BrewPackages) > 0 {
		fmt.Fprintf(&b, "RUN su - linuxbrew -c 'brew install %s'\n\n", strings.Join(profile.BrewPackages, " "))
	}

	if profile.PostInstall != "" {
		fmt.Fprintf(&b, "USER vm\n")
		fmt.Fprintf(&b, "RUN %s\n\n", profile.PostInstall)
	}

	fmt.Fprintf(&b, "USER root\n")
	fmt.Fprintf(&b, "RUN mkdir -p /usr/libexec/docker/cli-plugins && \\\n")
	fmt.Fprintf(&b, "    curl -fsSL \"https://github.com/docker/compose/releases/latest/download/docker-compose-linux-aarch64\" \\\n")
	fmt.Fprintf(&b, "      -o /usr/libexec/docker/cli-plugins/docker-compose && \\\n")
	fmt.Fprintf(&b, "    chmod 0755 /usr/libexec/docker/cli-plugins/docker-compose && \\\n")
	fmt.Fprintf(&b, "    curl -fsSL \"https://github.com/docker/buildx/releases/latest/download/buildx-v0.24.0.linux-arm64\" \\\n")
	fmt.Fprintf(&b, "      -o /usr/libexec/docker/cli-plugins/docker-buildx && \\\n")
	fmt.Fprintf(&b, "    chmod 0755 /usr/libexec/docker/cli-plugins/docker-buildx\n\n")

	fmt.Fprintf(&b, "USER vm\n")
	fmt.Fprintf(&b, "RUN git config --global core.editor %s && \\\n", editorCmd)
	fmt.Fprintf(&b, "    git config --global init.defaultBranch main && \\\n")
	fmt.Fprintf(&b, "    git config --global pull.rebase false && \\\n")
	fmt.Fprintf(&b, "    git config --global push.autoSetupRemote true\n")

	if cfg.GitUserName != "" {
		fmt.Fprintf(&b, "RUN git config --global user.name %q\n", cfg.GitUserName)
	}
	if cfg.GitUserEmail != "" {
		fmt.Fprintf(&b, "RUN git config --global user.email %q\n", cfg.GitUserEmail)
	}
	fmt.Fprintf(&b, "\n")

	if len(profile.Env) > 0 {
		for k, v := range profile.Env {
			fmt.Fprintf(&b, "ENV %s=%q\n", k, v)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "USER vm\n")
	fmt.Fprintf(&b, "RUN mkdir -p ~/repos ~/projects\n\n")

	fmt.Fprintf(&b, "COPY entrypoint.sh /entrypoint.sh\n")
	fmt.Fprintf(&b, "USER root\n")
	fmt.Fprintf(&b, "ENTRYPOINT [\"/entrypoint.sh\"]\n")
	fmt.Fprintf(&b, "CMD [\"sleep\", \"infinity\"]\n")

	return b.String(), nil
}

func GenerateEntrypoint() string {
	return "#!/bin/sh\nexec \"$@\"\n"
}

func WriteBuildContext(cfg Config, profile EnvironmentProfile) error {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return err
	}

	dockerfile, err := GenerateDockerfile(cfg, profile)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return err
	}

	entrypoint := GenerateEntrypoint()
	return os.WriteFile(filepath.Join(cfg.StateDir, "entrypoint.sh"), []byte(entrypoint), 0o755)
}

func shellPackage(shell string) string {
	switch shell {
	case "fish":
		return "fish"
	case "zsh":
		return "zsh"
	default:
		return "bash"
	}
}

func editorPackage(editor string) string {
	switch editor {
	case "helix":
		return "helix"
	default:
		return "neovim"
	}
}

func shellBinary(shell string) string {
	switch shell {
	case "fish":
		return "/usr/bin/fish"
	case "zsh":
		return "/bin/zsh"
	default:
		return "/bin/bash"
	}
}

func editorCommand(editor string) string {
	switch editor {
	case "helix":
		return "hx"
	default:
		return "nvim"
	}
}
