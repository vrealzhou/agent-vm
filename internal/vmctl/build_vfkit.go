package vmctl

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func resolveRootfsURL(cfg Config) (string, error) {
	cacheFile := filepath.Join(cfg.StateDir, "rootfs-url.cache")
	if data, err := os.ReadFile(cacheFile); err == nil {
		cached := strings.TrimSpace(string(data))
		if cached != "" {
			return cached, nil
		}
	}

	repoBase := strings.TrimRight(cfg.VoidRepository, "/") + "/live/current/"
	addProgress("resolving latest rootfs tarball from %s ...", repoBase)

	client := downloadHTTPClient(60 * time.Second)
	resp, err := client.Get(repoBase)
	if err != nil {
		return "", fmt.Errorf("failed to fetch live index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("live index returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read live index: %w", err)
	}

	re := regexp.MustCompile(`void-aarch64-ROOTFS-[^"']+\.tar\.xz`)
	matches := re.FindAllString(string(body), -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no void-aarch64-ROOTFS tarball found in live index")
	}

	pkg := matches[len(matches)-1]
	url := repoBase + pkg
	addProgress("resolved rootfs tarball: %s", pkg)

	if err := os.MkdirAll(cfg.StateDir, 0o755); err == nil {
		_ = os.WriteFile(cacheFile, []byte(url+"\n"), 0o644)
	}

	return url, nil
}
