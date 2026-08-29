package tproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/renameio/v2"
)

type Profile struct {
	Name        string `json:"name"`
	Secret      string `json:"secret"`
	Backend     string `json:"backend"`
	CarrierMode string `json:"carrier_mode"`
}

type ProfilesConfig struct {
	Profiles []Profile `json:"profiles"`
}

type Config struct {
	PublicHostname string `json:"public_hostname"`
	Listen         string `json:"listen"`
	AdminListen    string `json:"admin_listen"`
	PublicDir      string `json:"public_dir"`
	ProfilesFile   string `json:"profiles_file"`
	EnablePprof    bool   `json:"enable_pprof"`
}

func EnsureSiteIndex(publicDir string) error {
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		return fmt.Errorf("create public dir: %w", err)
	}

	indexPath := filepath.Join(publicDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		return nil
	}

	indexHTML := `<!DOCTYPE html><html><head><title>Edge Ingest Gateway</title></head><body><h3>Gateway Active</h3></body></html>`
	return renameio.WriteFile(indexPath, []byte(indexHTML), 0o644)
}

func WriteConfig(installDir, domain, secret string) error {
	tproxyDir := filepath.Join(installDir, "tproxy")
	if err := os.MkdirAll(tproxyDir, 0o700); err != nil {
		return fmt.Errorf("create tproxy dir: %w", err)
	}

	profilesFile := filepath.Join(tproxyDir, "profiles.json")
	configFile := filepath.Join(tproxyDir, "config.json")
	publicDir := "/srv/tproxy-site"

	profiles := ProfilesConfig{
		Profiles: []Profile{
			{
				Name:        "default",
				Secret:      secret,
				Backend:     "127.0.0.1:2398",
				CarrierMode: "https",
			},
		},
	}

	cfg := Config{
		PublicHostname: domain,
		Listen:         "127.0.0.1:8080",
		AdminListen:    "127.0.0.1:8081",
		PublicDir:      publicDir,
		ProfilesFile:   profilesFile,
		EnablePprof:    false,
	}

	profilesData, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profiles: %w", err)
	}

	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := renameio.WriteFile(profilesFile, profilesData, 0o600); err != nil {
		return fmt.Errorf("write profiles: %w", err)
	}

	if err := renameio.WriteFile(configFile, cfgData, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	_ = EnsureSiteIndex(publicDir)
	return nil
}

func MaintainTelegramSecrets(ctx context.Context, onUpdate func()) {
	update := func() {
		client := &http.Client{Timeout: 15 * time.Second}
		secretResp, err := client.Get("https://core.telegram.org/getProxySecret")
		if err == nil && secretResp.StatusCode == http.StatusOK {
			data, err := io.ReadAll(secretResp.Body)
			_ = secretResp.Body.Close()
			if err == nil && len(data) > 0 {
				_ = renameio.WriteFile("/etc/mtproxy/proxy-secret", data, 0o640)
			}
		}

		confResp, err := client.Get("https://core.telegram.org/getProxyConfig")
		if err == nil && confResp.StatusCode == http.StatusOK {
			data, err := io.ReadAll(confResp.Body)
			_ = confResp.Body.Close()
			if err == nil && len(data) > 0 {
				_ = renameio.WriteFile("/etc/mtproxy/proxy-multi.conf", data, 0o640)
			}
		}

		if onUpdate != nil {
			onUpdate()
		}
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

func DeriveCapability(domain, secretHex string) string {
	decoded, err := hex.DecodeString(strings.TrimSpace(secretHex))
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, decoded)
	_, _ = mac.Write([]byte("tdesktop-web-proxy-bridge-v1\n" + strings.ToLower(domain)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func BridgeURL(domain, secretHex string) string {
	cap := DeriveCapability(domain, secretHex)
	return fmt.Sprintf("https://%s/?bridge=%s", domain, cap)
}
