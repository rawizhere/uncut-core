package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type GitHubRelease struct {
	TagName string `json:"tag_name"`
}

func GetAvailableVersions(ctx context.Context, client *http.Client) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/shtorm-7/sing-box-extended/releases?per_page=15", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "uncut-core-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	versions := make([]string, 0, len(releases))
	for _, r := range releases {
		v := strings.TrimPrefix(r.TagName, "v")
		if v != "" {
			versions = append(versions, v)
		}
	}
	return versions, nil
}

func InstallSingboxVersion(ctx context.Context, version string, targetPath string) error {
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	arch := runtime.GOARCH
	downloadURL := fmt.Sprintf("https://github.com/shtorm-7/sing-box-extended/releases/download/%s/sing-box-%s-linux-%s.tar.gz", tag, strings.TrimPrefix(tag, "v"), arch)
	slog.Info("Downloading sing-box binary", "version", version, "url", downloadURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "uncut-core-updater")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip error: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	var foundBinary bool
	tmpTarget := targetPath + ".tmp"

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar error: %w", err)
		}

		if strings.HasSuffix(header.Name, "/sing-box") || header.Name == "sing-box" {
			outFile, err := os.OpenFile(tmpTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("create tmp binary: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return fmt.Errorf("write binary: %w", err)
			}
			_ = outFile.Close()
			foundBinary = true
			break
		}
	}

	if !foundBinary {
		_ = os.Remove(tmpTarget)
		return fmt.Errorf("sing-box binary not found in archive")
	}

	testCmd := exec.CommandContext(ctx, tmpTarget, "version")
	if err := testCmd.Run(); err != nil {
		_ = os.Remove(tmpTarget)
		return fmt.Errorf("verify downloaded binary: %w", err)
	}

	bakTarget := targetPath + ".bak"
	if _, err := os.Stat(targetPath); err == nil {
		_ = os.Rename(targetPath, bakTarget)
	}

	if err := os.Rename(tmpTarget, targetPath); err != nil {
		if _, bakErr := os.Stat(bakTarget); bakErr == nil {
			_ = os.Rename(bakTarget, targetPath)
		}
		return fmt.Errorf("replace binary: %w", err)
	}

	_ = os.Remove(bakTarget)
	slog.Info("Sing-Box binary updated successfully", "version", version, "path", targetPath)
	return nil
}

func GetCurrentSingboxVersion() string {
	cmd := exec.Command("sing-box", "version")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return "unknown"
}
