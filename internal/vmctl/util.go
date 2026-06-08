package vmctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pidIsRunning(pidFile string) (bool, error) {
	if !fileExists(pidFile) {
		return false, nil
	}
	pid, err := readPID(pidFile)
	if err != nil {
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, nil
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, nil
	}
	if state, err := processState(pid); err == nil {
		if strings.Contains(state, "Z") {
			return false, nil
		}
	}
	return true, nil
}

func processState(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runWithSignals(cmd *exec.Cmd) error {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	return cmd.Wait()
}

func discoverFirstFile(root, kind string) string {
	var exts []string
	switch kind {
	case "disk":
		exts = []string{".img", ".img.xz", ".qcow2", ".raw", ".raw.xz", ".tar.gz", ".tar.xz"}
	default:
		return ""
	}

	matches := []string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(rel, string(os.PathSeparator)) + 1
		if d.IsDir() {
			if depth > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > 3 {
			return nil
		}
		name := strings.ToLower(d.Name())
		for _, ext := range exts {
			if strings.HasSuffix(name, ext) {
				matches = append(matches, path)
				break
			}
		}
		return nil
	})

	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func remoteContentLength(rawURL string) int64 {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return -1
	}
	client := downloadHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return -1
}

func downloadFile(rawURL, destination string) error {
	tmp := destination + ".part"
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	logf("downloading %s", filepath.Base(destination))
	_ = os.Remove(tmp)

	if _, err := exec.LookPath("curl"); err == nil {
		if err := runCommand(
			"curl",
			"--fail",
			"--location",
			"--http1.1",
			"--progress-bar",
			"--retry", "5",
			"--retry-delay", "2",
			"--output", tmp,
			rawURL,
		); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, destination)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			logf("retrying download (%d/3)", attempt)
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		if err := downloadFileWithGo(rawURL, tmp); err != nil {
			lastErr = err
			_ = os.Remove(tmp)
			continue
		}
		if err := os.Rename(tmp, destination); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	return lastErr
}

func downloadFileWithGo(rawURL, tmp string) error {
	client := downloadHTTPClient(0)
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	total := resp.ContentLength
	var written int64
	lastPct := -1

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			wn, writeErr := out.Write(buf[:n])
			if writeErr != nil {
				out.Close()
				return writeErr
			}
			written += int64(wn)
			if total > 0 {
				pct := int(float64(written) / float64(total) * 100)
				if pct/10 != lastPct {
					lastPct = pct / 10
					addProgress("download: %d%% (%.1f MB / %.1f MB)", pct, float64(written)/1024/1024, float64(total)/1024/1024)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return readErr
		}
	}
	return out.Close()
}

func ensureDownloadedFile(rawURL, destination string) error {
	expectedSize := remoteContentLength(rawURL)
	if info, err := os.Stat(destination); err == nil {
		if expectedSize < 0 || info.Size() == expectedSize {
			return nil
		}
		logf("existing file size mismatch for %s: have %d, expected %d; re-downloading", filepath.Base(destination), info.Size(), expectedSize)
		if err := os.Remove(destination); err != nil {
			return err
		}
	}

	if err := downloadFile(rawURL, destination); err != nil {
		return err
	}
	if expectedSize > 0 {
		info, err := os.Stat(destination)
		if err != nil {
			return err
		}
		if info.Size() != expectedSize {
			return fmt.Errorf("downloaded file size mismatch for %s: have %d, expected %d", destination, info.Size(), expectedSize)
		}
	}
	return nil
}

func diskFormat(path string) (string, error) {
	cmd := exec.Command("qemu-img", "info", "--output", "json", path)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var payload struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return "", err
	}
	return payload.Format, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func isCompressedRawImage(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".img.xz") || strings.HasSuffix(lower, ".raw.xz")
}

func isVoidLinuxRootfsTarball(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tar.xz") && strings.Contains(lower, "void") && strings.Contains(lower, "rootfs")
}

func decompressXZToRaw(src, dst string) error {
	if _, err := exec.LookPath("xz"); err != nil {
		return fmt.Errorf("missing required command: xz")
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	cmd := exec.Command("xz", "--decompress", "--stdout")
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func parseSize(v string) (int64, error) {
	if v == "" {
		return 0, errors.New("empty size")
	}
	unit := v[len(v)-1]
	multiplier := int64(1)
	number := v
	switch unit {
	case 'K', 'k':
		multiplier = 1024
		number = v[:len(v)-1]
	case 'M', 'm':
		multiplier = 1024 * 1024
		number = v[:len(v)-1]
	case 'G', 'g':
		multiplier = 1024 * 1024 * 1024
		number = v[:len(v)-1]
	case 'T', 't':
		multiplier = 1024 * 1024 * 1024 * 1024
		number = v[:len(v)-1]
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return 0, err
	}
	return n * multiplier, nil
}

func downloadHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'"'"'`) + "'"
}

func writeBootstrapMarker(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.BootstrapMarker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.BootstrapMarker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
}

func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[agent-vm] %s\n", msg)
	addProgress("%s", msg)
}

func waitForSSH(cfg Config, user string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		cmd := exec.Command("ssh", append(sshArgsForUser(cfg, user), "true")...)
		if err := cmd.Run(); err == nil {
			return nil
		}
		if attempt%5 == 1 {
			addProgress("waiting for SSH on %s@%s...", user, cfg.StaticIP)
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timed out waiting for SSH on %s@%s", user, cfg.StaticIP)
}
