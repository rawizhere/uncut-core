package tproxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	domain := "vpn.example.com"
	secret := "ee0102030405060708090a0b0c0d0e0f76706e2e6578616d706c652e636f6d"

	if err := WriteConfig(tmpDir, domain, secret); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	profilesPath := filepath.Join(tmpDir, "tproxy", "profiles.json")
	configPath := filepath.Join(tmpDir, "tproxy", "config.json")

	pData, err := os.ReadFile(profilesPath)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	var profiles ProfilesConfig
	if err := json.Unmarshal(pData, &profiles); err != nil {
		t.Fatalf("unmarshal profiles: %v", err)
	}
	if len(profiles.Profiles) != 1 || profiles.Profiles[0].Secret != secret {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}

	cData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(cData, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.PublicHostname != domain || cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
