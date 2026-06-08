package vmctl

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Start(cfg Config) error {
	if _, err := exec.LookPath("utmctl"); err != nil {
		return fmt.Errorf("missing required command: utmctl (install UTM from https://mac.getutm.app)")
	}

	running, err := utmVMIsRunning(cfg.Name)
	if err != nil {
		return err
	}
	if running {
		logf("VM is already running")
		return Status(cfg)
	}

	cfg, err = prepareDisk(cfg)
	if err != nil {
		return err
	}

	voidBootstrapCandidate := isVoidLinuxRootfsTarball(cfg.BaseImage)

	logf("starting %s", cfg.Name)
	addProgress("starting VM via UTM...")
	if err := exec.Command("utmctl", "start", cfg.Name).Run(); err != nil {
		return fmt.Errorf("utmctl start failed: %w", err)
	}

	addProgress("waiting for VM to reach running state...")
	if err := waitForUTMRunning(cfg.Name, 90*time.Second); err != nil {
		return fmt.Errorf("VM did not reach running state")
	}

	logf("VM started")
	if voidBootstrapCandidate && !fileExists(cfg.BootstrapMarker) {
		logf("waiting for root SSH to fix guest configuration...")
		if err := waitForSSH(cfg, "root", 3*time.Minute); err != nil {
			return err
		}
		if err := fixGuestConfig(cfg); err != nil {
			logf("fix guest config: %v", err)
		}
		logf("waiting for SSH so first-boot bootstrap can finish")
		addProgress("waiting for VM SSH to become available...")
		if err := waitForSSH(cfg, cfg.SSHUser, 5*time.Minute); err != nil {
			addProgress("guest SSH for %s not ready, retrying root repair...", cfg.SSHUser)
			if repairErr := fixGuestConfig(cfg); repairErr != nil {
				logf("retry fix guest config: %v", repairErr)
			}
			if retryErr := waitForSSH(cfg, cfg.SSHUser, 90*time.Second); retryErr != nil {
				return retryErr
			}
		}
		addProgress("SSH available, running bootstrap...")
		if err := Bootstrap(cfg); err != nil {
			return err
		}
		if err := os.WriteFile(cfg.BootstrapMarker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
			return err
		}
		addProgress("bootstrap complete")
	}
	if err := StartAutoTunnels(cfg); err != nil {
		logf("auto-start tunnels: %v", err)
	}
	return Status(cfg)
}

func Stop(cfg Config) error {
	running, err := utmVMIsRunning(cfg.Name)
	if err != nil {
		return err
	}
	if !running {
		logf("VM is not running")
		return nil
	}

	logf("stopping %s", cfg.Name)
	if err := exec.Command("utmctl", "stop", cfg.Name).Run(); err != nil {
		return fmt.Errorf("utmctl stop failed: %w", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		running, err = utmVMIsRunning(cfg.Name)
		if err != nil {
			return err
		}
		if !running {
			logf("VM stopped")
			if err := StopAllTunnels(cfg); err != nil {
				logf("stop tunnels: %v", err)
			}
			return nil
		}
		time.Sleep(time.Second)
	}

	return fmt.Errorf("VM did not stop in time")
}

func Status(cfg Config) error {
	backend := NewBackend(cfg)
	status, err := backend.Status(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("name: %s\n", status.Name)
	fmt.Printf("backend: %s\n", cfg.Backend)
	fmt.Printf("state: %s\n", status.State)
	if cfg.Backend == "" || cfg.Backend == "utm" {
		fmt.Printf("disk: %s\n", status.DiskPath)
		fmt.Printf("ip: %s\n", status.StaticIP)
	}
	fmt.Printf("bootstrap: %t\n", status.BootstrapDone)
	if status.Running {
		if cfg.Backend == "" || cfg.Backend == "utm" {
			fmt.Printf("ssh: ssh %s\n", status.SSHTarget)
		} else {
			fmt.Printf("container: %s\n", cfg.ContainerName)
			fmt.Printf("image: %s\n", cfg.Image)
		}
	}
	return nil
}

func SSH(cfg Config, extraArgs []string) error {
	args := sshArgs(cfg)
	args = append(args, extraArgs...)
	cmd := exec.Command("ssh", args...)
	return runWithSignals(cmd)
}

func Bootstrap(cfg Config) error {
	script, err := generateBootstrapScript(cfg)
	if err != nil {
		return fmt.Errorf("failed to generate bootstrap script: %w", err)
	}

	scriptsDir := filepath.Join(cfg.ConfigDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create scripts dir: %w", err)
	}
	scriptPath := filepath.Join(scriptsDir, "guest-bootstrap.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("failed to write bootstrap script: %w", err)
	}

	logf("configuring %s + %s + %s inside %s", cfg.DefaultShell, cfg.DefaultEditor, cfg.WindowManager, cfg.Name)
	addProgress("running bootstrap script (this may take several minutes)...")

	cmd := exec.Command("ssh", append(sshArgsForUser(cfg, cfg.SSHUser), "bash -s")...)
	cmd.Stdin = strings.NewReader(script)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	scannerOut := bufio.NewScanner(stdout)
	scannerErr := bufio.NewScanner(stderr)
	go func() {
		for scannerOut.Scan() {
			addProgress("%s", scannerOut.Text())
		}
	}()
	go func() {
		for scannerErr.Scan() {
			addProgress("%s", scannerErr.Text())
		}
	}()
	if err := cmd.Wait(); err != nil {
		addProgress("bootstrap script failed: %v", err)
		return err
	}
	return nil
}

func BootstrapSetup(cfg Config) error {
	addProgress("starting bootstrap setup...")
	status, err := InspectVM(cfg)
	if err != nil {
		return err
	}

	if !status.Running {
		if err := Start(cfg); err != nil {
			return err
		}
		if fileExists(cfg.BootstrapMarker) {
			addProgress("VM started, bootstrap already done")
			return nil
		}
	}

	addProgress("waiting for SSH to run bootstrap...")
	if err := waitForSSH(cfg, cfg.SSHUser, 5*time.Minute); err != nil {
		addProgress("guest SSH for %s not ready, attempting root-side repair...", cfg.SSHUser)
		if repairErr := fixGuestConfig(cfg); repairErr != nil {
			logf("fix guest config: %v", repairErr)
		}
		if retryErr := waitForSSH(cfg, cfg.SSHUser, 90*time.Second); retryErr != nil {
			return retryErr
		}
	}
	addProgress("running bootstrap script...")
	if err := Bootstrap(cfg); err != nil {
		return err
	}
	addProgress("bootstrap setup complete")
	if err := writeBootstrapMarker(cfg); err != nil {
		return err
	}
	addProgress("restarting VM to apply bootstrap changes...")
	_ = rebootVM(cfg)
	return nil
}

func rebootVM(cfg Config) error {
	cmd := exec.Command("ssh", append(sshArgs(cfg), "reboot")...)
	if err := cmd.Run(); err != nil {
		return err
	}
	return waitForSSH(cfg, cfg.SSHUser, 2*time.Minute)
}

func UpgradeKernel(cfg Config) (string, error) {
	if err := waitForSSH(cfg, cfg.SSHUser, 60*time.Second); err != nil {
		return "", fmt.Errorf("SSH not ready: %w", err)
	}

	upgradeCmd := "xbps-install -uy linux6.12 && xbps-reconfigure -f linux6.12"
	cmd := exec.Command("ssh", append(sshArgs(cfg), upgradeCmd)...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("kernel upgrade failed: %w", err)
	}
	scannerOut := bufio.NewScanner(stdout)
	scannerErr := bufio.NewScanner(stderr)
	go func() {
		for scannerOut.Scan() {
			addProgress("%s", scannerOut.Text())
		}
	}()
	go func() {
		for scannerErr.Scan() {
			addProgress("%s", scannerErr.Text())
		}
	}()
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("kernel upgrade failed: %w", err)
	}

	findKernel := "ls -1 /boot/vmlinux-* /boot/vmlinuz-* 2>/dev/null | sort | tail -1"
	kernelOut, err := exec.Command("ssh", append(sshArgs(cfg), findKernel)...).Output()
	if err != nil {
		return "", fmt.Errorf("failed to find kernel: %w", err)
	}
	kernelPath := strings.TrimSpace(string(kernelOut))
	if kernelPath == "" {
		return "", fmt.Errorf("no kernel found in /boot")
	}

	findInitrd := "ls -1 /boot/initramfs-*.img 2>/dev/null | sort | tail -1"
	initrdOut, err := exec.Command("ssh", append(sshArgs(cfg), findInitrd)...).Output()
	if err != nil {
		return "", fmt.Errorf("failed to find initrd: %w", err)
	}
	initrdPath := strings.TrimSpace(string(initrdOut))
	if initrdPath == "" {
		return "", fmt.Errorf("no initramfs found in /boot")
	}

	if err := copyRemoteFile(cfg, kernelPath, cfg.KernelPath); err != nil {
		return "", fmt.Errorf("failed to copy kernel: %w", err)
	}
	if err := copyRemoteFile(cfg, initrdPath, cfg.InitrdPath); err != nil {
		return "", fmt.Errorf("failed to copy initrd: %w", err)
	}

	version := filepath.Base(kernelPath)

	if err := Stop(cfg); err != nil {
		return version, fmt.Errorf("kernel updated but stop failed: %w", err)
	}
	time.Sleep(2 * time.Second)
	if err := Start(cfg); err != nil {
		return version, fmt.Errorf("kernel updated but start failed: %w", err)
	}

	return version, nil
}

func copyRemoteFile(cfg Config, remotePath, localPath string) error {
	tmpPath := localPath + ".new"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	cmd := exec.Command("ssh", append(sshArgs(cfg), "cat "+shellQuote(remotePath))...)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, localPath)
}

func prepareDisk(cfg Config) (Config, error) {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return cfg, err
	}

	if fileExists(cfg.DiskPath) {
		if err := ensureUTMBundle(cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}

	if err := ensureUTMBundle(cfg); err != nil {
		return cfg, err
	}

	addProgress("preparing VM disk for first boot...")
	addProgress("resolving base image...")
	cfg, err := resolveBaseImage(cfg)
	if err != nil {
		return cfg, err
	}

	if err := createDiskFromBaseImage(cfg); err != nil {
		return cfg, err
	}
	addProgress("VM disk ready")
	return cfg, nil
}

func resolveBaseImage(cfg Config) (Config, error) {
	if cfg.BaseImage == "" {
		cfg.BaseImage = discoverFirstFile(cfg.ImageDir, "disk")
	}
	if cfg.BaseImage == "" {
		matches, _ := filepath.Glob(filepath.Join(cfg.ImageDir, "void-aarch64-ROOTFS-*.tar.xz"))
		if len(matches) == 1 {
			cfg.BaseImage = matches[0]
		}
	}
	if cfg.BaseImage == "" {
		cfg.BaseImage = filepath.Join(cfg.ImageDir, "void-aarch64-ROOTFS.tar.xz")
	}
	if fileExists(cfg.BaseImage) {
		addProgress("base image found: %s", filepath.Base(cfg.BaseImage))
		return cfg, nil
	}
	if cfg.BaseImageURL == "" {
		url, err := resolveRootfsURL(cfg)
		if err != nil {
			return cfg, fmt.Errorf("VM_BASE_IMAGE does not exist and auto-resolve failed: %w", err)
		}
		cfg.BaseImageURL = url
	}
	expectedSize := remoteContentLength(cfg.BaseImageURL)
	if expectedSize > 0 {
		addProgress("downloading base image (%.0f MB)...", float64(expectedSize)/1024/1024)
	} else {
		addProgress("downloading base image...")
	}
	if err := ensureDownloadedFile(cfg.BaseImageURL, cfg.BaseImage); err != nil {
		return cfg, err
	}
	addProgress("base image downloaded")
	return cfg, nil
}

func createDiskFromBaseImage(cfg Config) error {
	if !fileExists(cfg.BaseImage) {
		return fmt.Errorf("VM_BASE_IMAGE does not exist: %s", cfg.BaseImage)
	}
	if isVoidLinuxRootfsTarball(cfg.BaseImage) {
		return buildVoidLinuxDisk(cfg)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return fmt.Errorf("missing required command: qemu-img")
	}
	if isCompressedRawImage(cfg.BaseImage) {
		return createDiskFromCompressedRaw(cfg)
	}
	return createDiskFromImageFile(cfg)
}

func createDiskFromCompressedRaw(cfg Config) error {
	logf("creating VM disk from compressed raw base image")
	addProgress("decompressing base image...")
	if err := decompressXZToRaw(cfg.BaseImage, cfg.DiskPath); err != nil {
		return err
	}
	addProgress("resizing disk to %s...", cfg.DiskSize)
	return resizeRawDisk(cfg)
}

func createDiskFromImageFile(cfg Config) error {
	format, err := diskFormat(cfg.BaseImage)
	if err != nil {
		return err
	}
	logf("creating VM disk from base image (%s)", format)
	addProgress("converting %s base image to raw disk...", format)
	if format == "raw" {
		if err := copyFile(cfg.BaseImage, cfg.DiskPath); err != nil {
			return err
		}
	} else {
		if err := runCommand("qemu-img", "convert", "-f", format, "-O", "raw", cfg.BaseImage, cfg.DiskPath); err != nil {
			return err
		}
	}
	return resizeRawDisk(cfg)
}

func resizeRawDisk(cfg Config) error {
	return runCommand("qemu-img", "resize", "-f", "raw", cfg.DiskPath, cfg.DiskSize)
}

func sshArgs(cfg Config) []string {
	return sshArgsForUser(cfg, cfg.SSHUser)
}

func sshArgsForUser(cfg Config, user string) []string {
	args := []string{}
	if cfg.SSHKnownHostsFile != "" {
		args = append(args,
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "UserKnownHostsFile="+cfg.SSHKnownHostsFile,
		)
	} else {
		args = append(args,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		)
	}
	privKey := cfg.SSHPrivateKey
	if privKey == "" {
		privKey = strings.TrimSuffix(cfg.SSHPublicKey, ".pub")
	}
	if fileExists(privKey) {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", privKey)
	}
	args = append(args, user+"@"+cfg.StaticIP)
	return args
}

func buildVoidLinuxDisk(cfg Config) error {
	if !fileExists(cfg.SSHPublicKey) {
		return fmt.Errorf("VM_SSH_PUBLIC_KEY does not exist: %s", cfg.SSHPublicKey)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DiskPath), 0o755); err != nil {
		return err
	}

	if _, err := exec.LookPath("podman"); err != nil {
		return fmt.Errorf("missing required command: podman (needed for Void Linux disk build)")
	}
	logf("building Void Linux VM disk using podman")
	addProgress("building Void Linux VM disk (this takes several minutes)...")
	builder := exec.Command(
		"podman", "run", "--rm", "--platform", "linux/arm64",
		"-e", "DISK_SIZE="+cfg.DiskSize,
		"-e", "STATIC_IP="+cfg.StaticIP,
		"-e", "CIDR="+fmt.Sprintf("%d", cfg.CIDR),
		"-e", "GATEWAY="+cfg.Gateway,
		"-e", "DNS_SERVERS="+cfg.DNSServers,
		"-e", "VM_MAC="+cfg.MAC,
		"-e", "GUEST_USER="+cfg.GuestUser,
		"-e", "GUEST_PASSWORD="+cfg.GuestPassword,
		"-e", "ROOT_PASSWORD="+cfg.RootPassword,
		"-e", "TIMEZONE="+cfg.Timezone,
		"-e", "VOID_REPOSITORY="+cfg.VoidRepository,
		"-e", "VM_NAME="+cfg.Name,
		"-e", "DEFAULT_SHELL="+cfg.DefaultShell,
		"-e", "DEFAULT_EDITOR="+cfg.DefaultEditor,
		"-e", "WINDOW_MANAGER="+cfg.WindowManager,
		"-v", cfg.ImageDir+":/repo:ro",
		"-v", filepath.Dir(cfg.DiskPath)+":/work",
		"-v", cfg.BaseImage+":/input/base.tar.xz:ro",
		"-v", cfg.SSHPublicKey+":/input/authorized_key.pub:ro",
		"docker.io/library/debian:stable-slim",
		"bash", "-lc", voidLinuxBuildScript(cfg),
	)
	builder.Stdout = os.Stdout
	builder.Stderr = os.Stderr
	return builder.Run()
}

func waitForUTMRunning(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := utmVMIsRunning(name)
		if err == nil && running {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for VM %s to start", name)
}

func fixGuestConfig(cfg Config) error {
	pubKey, err := os.ReadFile(cfg.SSHPublicKey)
	if err != nil {
		return err
	}
	key := shellQuote(strings.TrimSpace(string(pubKey)))
	guest := shellQuote(cfg.GuestUser)
	script := fmt.Sprintf(`set -e
guest=%s
pubkey=%s

mkdir -p /home/"${guest}"/.ssh /home/"${guest}"/.config/fish/conf.d
printf '%%s\n' "${pubkey}" > /home/"${guest}"/.ssh/authorized_keys
chmod 700 /home/"${guest}"/.ssh
chmod 600 /home/"${guest}"/.ssh/authorized_keys

cat > /home/"${guest}"/.bash_profile <<'EOF'
export XDG_RUNTIME_DIR="${HOME}/.local/run"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
if [ -z "${DISPLAY:-}" ] && [ "$(tty 2>/dev/null)" = "/dev/tty1" ]; then
  exec /usr/local/bin/vmctl-session
fi
EOF

cat > /home/"${guest}"/.zprofile <<'EOF'
export XDG_RUNTIME_DIR="${HOME}/.local/run"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
if [ -z "${DISPLAY:-}" ] && [ "$(tty 2>/dev/null)" = "/dev/tty1" ]; then
  exec /usr/local/bin/vmctl-session
fi
EOF

cat > /home/"${guest}"/.config/fish/conf.d/vmctl-session.fish <<'EOF'
if status is-interactive
  if test -z "$DISPLAY"
    if string match -q /dev/tty1 (tty 2>/dev/null)
      exec /usr/local/bin/vmctl-session
    end
  end
end
EOF

chown -R "${guest}:${guest}" /home/"${guest}" 2>/dev/null || true
passwd -d "${guest}" >/dev/null 2>&1 || true
usermod -U "${guest}" >/dev/null 2>&1 || true
grep -q '^PerSourcePenalties no$' /etc/ssh/sshd_config.d/99-vmctl.conf || printf '\nPerSourcePenalties no\n' >> /etc/ssh/sshd_config.d/99-vmctl.conf
sv restart sshd >/dev/null 2>&1 || true

test -s /home/"${guest}"/.ssh/authorized_keys
test -s /home/"${guest}"/.bash_profile
test -s /home/"${guest}"/.config/fish/conf.d/vmctl-session.fish
echo DONE
`, guest, key)

	cmd := exec.Command("ssh", append(sshArgsForUser(cfg, "root"), "sh -s")...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fixGuestConfig: %w\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "DONE") {
		return fmt.Errorf("fixGuestConfig incomplete:\n%s", string(out))
	}
	logf("guest configuration repaired")
	return nil
}

func ensureUTMBundle(cfg Config) error {
	if fileExists(filepath.Join(cfg.UTMBundlePath, "config.plist")) {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(cfg.UTMBundlePath, "Data"), 0o755); err != nil {
		return err
	}
	return writeUTMConfigPlist(cfg)
}

func writeUTMConfigPlist(cfg Config) error {
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>system</key>
	<dict>
		<key>architecture</key><string>aarch64</string>
		<key>backend</key><string>qemu</string>
		<key>cpuCount</key><integer>%d</integer>
		<key>memory</key><integer>%d</integer>
	</dict>
	<key>display</key>
	<dict>
		<key>hardware</key><string>virtio-gpu-pci</string>
	</dict>
	<key>drives</key>
	<array>
		<dict>
			<key>imageName</key><string>disk.img</string>
			<key>interface</key><string>virtio</string>
			<key>size</key><integer>%d</integer>
		</dict>
	</array>
	<key>network</key>
	<array>
		<dict>
			<key>hardware</key><string>virtio-net-pci</string>
			<key>mode</key><string>shared</string>
		</dict>
	</array>
	<key>input</key>
	<dict>
		<key>keyboard</key><true/>
		<key>pointer</key><true/>
	</dict>
	<key>sharing</key>
	<dict>
		<key>clipboardSharing</key><true/>
	</dict>
</dict>
</plist>
`, cfg.CPUs, cfg.MemoryMiB, 0)

	return os.WriteFile(filepath.Join(cfg.UTMBundlePath, "config.plist"), []byte(plist), 0o644)
}

func voidLinuxBuildScript(cfg Config) string {
	return `#!/bin/bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

mkdir -p /tmp/apt-sources
cat >/tmp/apt-sources/vmctl.sources <<'EOF'
Types: deb
URIs: http://deb.debian.org/debian
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
EOF

apt-get \
  -o Dir::Etc::sourcelist=/dev/null \
  -o Dir::Etc::sourceparts=/tmp/apt-sources \
  -o Acquire::Check-Date=false \
  -o Acquire::Check-Valid-Until=false \
  update >/dev/null
apt-get \
  -o Dir::Etc::sourcelist=/dev/null \
  -o Dir::Etc::sourceparts=/tmp/apt-sources \
  -o Acquire::Check-Date=false \
  -o Acquire::Check-Valid-Until=false \
  install -y xz-utils ca-certificates e2fsprogs openssl >/dev/null

rm -f /work/disk.img /work/vmlinuz /work/initramfs.img
rm -rf /tmp/void-rootfs
mkdir -p /tmp/void-rootfs

tar -xJf /input/base.tar.xz -C /tmp/void-rootfs
cp /etc/resolv.conf /tmp/void-rootfs/etc/resolv.conf

retry_chroot_xbps() {
  local cmd="$1"
  local attempt=0
  while [ "${attempt}" -lt 8 ]; do
    attempt=$((attempt + 1))
    printf '[vmctl-build] xbps attempt %s: %s\n' "${attempt}" "${cmd}"
    if chroot /tmp/void-rootfs /bin/sh -lc "${cmd}"; then
      return 0
    fi
    sleep 10
  done
  return 1
}

repo="${VOID_REPOSITORY%/}/current"
mkdir -p /tmp/void-rootfs/etc/xbps.d
cat >/tmp/void-rootfs/etc/xbps.d/00-vmctl-repository.conf <<EOF
repository=${repo}
EOF

retry_chroot_xbps "xbps-install -R ${repo} -Sy xbps && xbps-install -R ${repo} -uy xbps"
retry_chroot_xbps "DRACUT_NO_XATTR=1 xbps-install -R ${repo} -Suy linux6.12 dracut openssh NetworkManager dbus fish-shell zsh curl wget git unzip bash file sudo chrony neovim helix docker make"
retry_chroot_xbps "xbps-install -R ${repo} -Suy xorg xfce4 xfce4-terminal lxqt openbox ghostty ghostty-terminfo mesa mesa-dri fcitx5 fcitx5-chinese-addons fcitx5-configtool fcitx5-gtk+2 fcitx5-gtk+3 fcitx5-gtk4 fcitx5-qt5 fcitx5-qt6 noto-fonts-cjk noto-fonts-emoji font-sarasa-gothic spice-vdagent"
retry_chroot_xbps "xbps-install -R ${repo} -Suy chromium"

printf '%s\n' "${VM_NAME}" >/tmp/void-rootfs/etc/hostname

mkdir -p /tmp/void-rootfs/etc/ssh/sshd_config.d
cat >/tmp/void-rootfs/etc/ssh/sshd_config.d/99-vmctl.conf <<SSH
PermitRootLogin prohibit-password
PasswordAuthentication no
KbdInteractiveAuthentication no
PerSourcePenalties no
SSH

guest_shell="/bin/bash"
case "${DEFAULT_SHELL}" in
  fish) guest_shell="/usr/bin/fish" ;;
  zsh) guest_shell="/usr/bin/zsh" ;;
esac

if ! chroot /tmp/void-rootfs /usr/bin/id -u "${GUEST_USER}" >/dev/null 2>&1; then
  chroot /tmp/void-rootfs /usr/sbin/useradd -m -G wheel,audio,video,input,docker -s "${guest_shell}" "${GUEST_USER}"
else
  chroot /tmp/void-rootfs /usr/sbin/usermod -aG wheel,audio,video,input,docker "${GUEST_USER}"
  chroot /tmp/void-rootfs /usr/sbin/usermod -s "${guest_shell}" "${GUEST_USER}"
fi

if ! chroot /tmp/void-rootfs /usr/bin/getent group chrony >/dev/null 2>&1; then
  chroot /tmp/void-rootfs /usr/sbin/groupadd -r chrony
fi
if ! chroot /tmp/void-rootfs /usr/bin/id -u chrony >/dev/null 2>&1; then
  chroot /tmp/void-rootfs /usr/sbin/useradd -r -M -g chrony -s /bin/false chrony
fi

root_hash="$(openssl passwd -6 "${ROOT_PASSWORD}")"
guest_hash="$(openssl passwd -6 "${GUEST_PASSWORD}")"
chroot /tmp/void-rootfs /usr/sbin/usermod -p "${root_hash}" root
chroot /tmp/void-rootfs /usr/sbin/usermod -p "${guest_hash}" "${GUEST_USER}"

install -d -m 700 /tmp/void-rootfs/root/.ssh
install -d -m 700 /tmp/void-rootfs/home/"${GUEST_USER}"/.ssh
install -m 600 /input/authorized_key.pub /tmp/void-rootfs/root/.ssh/authorized_keys
install -m 600 /input/authorized_key.pub /tmp/void-rootfs/home/"${GUEST_USER}"/.ssh/authorized_keys

chroot /tmp/void-rootfs /usr/bin/chown -R root:root /root/.ssh
chroot /tmp/void-rootfs /usr/bin/chown -R "${GUEST_USER}:${GUEST_USER}" /home/"${GUEST_USER}"/.ssh

mkdir -p /tmp/void-rootfs/etc/sudoers.d
cat >/tmp/void-rootfs/etc/sudoers.d/10-vmctl <<SUDO
%wheel ALL=(ALL) NOPASSWD: ALL
SUDO
chmod 0440 /tmp/void-rootfs/etc/sudoers.d/10-vmctl

mkdir -p /tmp/void-rootfs/etc/NetworkManager/system-connections
cat >/tmp/void-rootfs/etc/NetworkManager/system-connections/vmctl.nmconnection <<NM
[connection]
id=vmctl
type=ethernet
autoconnect=true

[ethernet]
mac-address=${VM_MAC}

[ipv4]
method=manual
address1=${STATIC_IP}/${CIDR},${GATEWAY}
dns=${DNS_SERVERS//,/;}

[ipv6]
method=ignore
NM
chmod 0600 /tmp/void-rootfs/etc/NetworkManager/system-connections/vmctl.nmconnection

mkdir -p /tmp/void-rootfs/etc/NetworkManager/conf.d
cat >/tmp/void-rootfs/etc/NetworkManager/conf.d/10-vmctl.conf <<'EOF'
[main]
dns=none
EOF

if [ -n "${TIMEZONE:-}" ] && [ -e "/tmp/void-rootfs/usr/share/zoneinfo/${TIMEZONE}" ]; then
  ln -snf "/usr/share/zoneinfo/${TIMEZONE}" /tmp/void-rootfs/etc/localtime
  printf '%s\n' "${TIMEZONE}" >/tmp/void-rootfs/etc/timezone
fi

{
  printf '# Generated by vmctl\n'
  oldIFS="${IFS}"
  IFS=,
  for ns in ${DNS_SERVERS}; do
    printf 'nameserver %s\n' "${ns}"
  done
  IFS="${oldIFS}"
} >/tmp/void-rootfs/etc/resolv.conf

mkdir -p /tmp/void-rootfs/usr/local/bin
cat >/tmp/void-rootfs/usr/local/bin/vmctl-session <<EOF
#!/bin/sh
export GTK_IM_MODULE=fcitx
export QT_IM_MODULE=fcitx
export SDL_IM_MODULE=fcitx
export XMODIFIERS=@im=fcitx
export XDG_RUNTIME_DIR="${HOME}/.local/run"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
case "${WINDOW_MANAGER}" in
  xfce)
    export XDG_CURRENT_DESKTOP=XFCE
    export XDG_SESSION_DESKTOP=xfce
    export XDG_SESSION_TYPE=x11
    if [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
      exec dbus-run-session startxfce4
    fi
    exec startxfce4
    ;;
  *)
    export XDG_CURRENT_DESKTOP=LXQt
    export XDG_SESSION_DESKTOP=lxqt
    export XDG_SESSION_TYPE=x11
    if [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
      exec dbus-run-session startlxqt
    fi
    exec startlxqt
    ;;
esac
EOF
chmod 0755 /tmp/void-rootfs/usr/local/bin/vmctl-session

cat >/tmp/void-rootfs/usr/local/bin/vmctl-chromium <<'EOF'
#!/bin/sh
export GTK_IM_MODULE=fcitx
export XMODIFIERS=@im=fcitx
exec /usr/bin/chromium --ozone-platform=x11 "$@"
EOF
chmod 0755 /tmp/void-rootfs/usr/local/bin/vmctl-chromium

mkdir -p /tmp/void-rootfs/etc/runit/runsvdir/default
for svc in dbus sshd NetworkManager chronyd docker spice-vdagentd; do
  if [ -d "/tmp/void-rootfs/etc/sv/${svc}" ]; then
    ln -snf "/etc/sv/${svc}" "/tmp/void-rootfs/etc/runit/runsvdir/default/${svc}"
  fi
done
cat >/tmp/void-rootfs/etc/sv/agetty-tty1/conf <<EOF
if [ -x /sbin/agetty -o -x /bin/agetty ]; then
	GETTY_ARGS="--autologin ${GUEST_USER} --noclear"
fi

BAUD_RATE=38400
TERM_NAME=linux
EOF

cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.bash_profile <<'EOF'
export XDG_RUNTIME_DIR="${HOME}/.local/run"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
if [ -z "${DISPLAY:-}" ] && [ "$(tty 2>/dev/null)" = "/dev/tty1" ]; then
  exec /usr/local/bin/vmctl-session
fi
EOF
chroot /tmp/void-rootfs /usr/bin/chown "${GUEST_USER}:${GUEST_USER}" /home/"${GUEST_USER}"/.bash_profile

mkdir -p /tmp/void-rootfs/home/"${GUEST_USER}"/.config/fish/conf.d
cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.config/fish/conf.d/vmctl-session.fish <<'EOF'
if status is-interactive
  if test -z "$DISPLAY"
    if string match -q /dev/tty1 (tty 2>/dev/null)
      exec /usr/local/bin/vmctl-session
    end
  end
end
EOF
cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.zprofile <<'EOF'
export XDG_RUNTIME_DIR="${HOME}/.local/run"
mkdir -p "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
if [ -z "${DISPLAY:-}" ] && [ "$(tty 2>/dev/null)" = "/dev/tty1" ]; then
  exec /usr/local/bin/vmctl-session
fi
EOF
chroot /tmp/void-rootfs /usr/bin/chown -R "${GUEST_USER}:${GUEST_USER}" /home/"${GUEST_USER}"/.config/fish /home/"${GUEST_USER}"/.zprofile

mkdir -p /tmp/void-rootfs/home/"${GUEST_USER}"/.config/fcitx5
mkdir -p /tmp/void-rootfs/home/"${GUEST_USER}"/.config/fcitx5/conf
cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.config/fcitx5/config <<'EOF'
[Hotkey]
EnumerateWithTriggerKeys=True
EnumerateSkipFirst=False
ModifierOnlyKeyTimeout=250

[Hotkey/TriggerKeys]
0=Shift_L

[Hotkey/AltTriggerKeys]
0=Caps_Lock

[Hotkey/EnumerateForwardKeys]
0=Shift_L

[Hotkey/PrevPage]
0=Up

[Hotkey/NextPage]
0=Down

[Hotkey/PrevCandidate]
0=Shift+Tab

[Hotkey/NextCandidate]
0=Tab

[Behavior]
ActiveByDefault=False
resetStateWhenFocusIn=No
ShareInputState=No
PreeditEnabledByDefault=True
ShowInputMethodInformation=True
showInputMethodInformationWhenFocusIn=False
CompactInputMethodInformation=True
ShowFirstInputMethodInformation=True
DefaultPageSize=5
EnabledAddons=
DisabledAddons=
PreloadInputMethod=True
OverrideXkbOption=False
CustomXkbOption=
AllowInputMethodForPassword=False
ShowPreeditForPassword=False
AutoSavePeriod=30
EOF
cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.config/fcitx5/profile <<'EOF'
[Groups/0]
Name=Default
Default Layout=us
DefaultIM=pinyin

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[Groups/0/Items/1]
Name=pinyin
Layout=

[GroupOrder]
0=Default
EOF
chroot /tmp/void-rootfs /usr/bin/chown -R "${GUEST_USER}:${GUEST_USER}" /home/"${GUEST_USER}"/.config/fcitx5

mkdir -p /tmp/void-rootfs/home/"${GUEST_USER}"/.local/share/applications
cat >/tmp/void-rootfs/home/"${GUEST_USER}"/.local/share/applications/chromium.desktop <<'EOF'
[Desktop Entry]
Version=1.0
Name=Chromium
GenericName=Web Browser
Comment=Access the Internet
Exec=/usr/local/bin/vmctl-chromium %U
StartupNotify=true
Terminal=false
Icon=chromium
Type=Application
Categories=Network;WebBrowser;
MimeType=application/pdf;application/rdf+xml;application/rss+xml;application/xhtml+xml;application/xhtml_xml;application/xml;image/gif;image/jpeg;image/png;image/webp;text/html;text/xml;x-scheme-handler/http;x-scheme-handler/https;x-scheme-handler/chromium;
Actions=new-window;new-private-window;

[Desktop Action new-window]
Name=New Window
Exec=/usr/local/bin/vmctl-chromium

[Desktop Action new-private-window]
Name=New Incognito Window
Exec=/usr/local/bin/vmctl-chromium --incognito
EOF
chroot /tmp/void-rootfs /usr/bin/chown -R "${GUEST_USER}:${GUEST_USER}" /home/"${GUEST_USER}"/.local/share/applications

chroot /tmp/void-rootfs /bin/sh -lc "DRACUT_NO_XATTR=1 xbps-reconfigure -fa || true"

kernel="$(
  find /tmp/void-rootfs/boot -maxdepth 1 -type f \( -name 'vmlinux-*' -o -name 'Image*' -o -name 'vmlinuz-*' \) \
    | sort | tail -n 1
)"
initrd="$(find /tmp/void-rootfs/boot -maxdepth 1 -type f -name 'initramfs-*.img' | sort | tail -n 1)"
if [ -z "${kernel}" ] || [ -z "${initrd}" ]; then
  printf 'missing boot assets after Void provisioning\n' >&2
  find /tmp/void-rootfs/boot -maxdepth 2 -type f | sort >&2 || true
  exit 1
fi

cp "${kernel}" /work/vmlinuz
cp "${initrd}" /work/initramfs.img

truncate -s "${DISK_SIZE}" /work/disk.img
mkfs.ext4 -F -L rootfs -d /tmp/void-rootfs /work/disk.img
`
}
