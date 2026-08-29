package setup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
)

func TestEnsureAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	appCfg := &config.AppConfig{
		Domain:  "test.example.com",
		Email:   "admin@example.com",
		Country: "DE",
	}

	opts := DefaultOptions(tmpDir, tmpDir)
	settings, err := Ensure(context.Background(), store, appCfg, opts)
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	if settings.Domain != "test.example.com" {
		t.Fatalf("expected domain test.example.com, got %s", settings.Domain)
	}
	if settings.Email != "admin@example.com" {
		t.Fatalf("expected email admin@example.com, got %s", settings.Email)
	}
	if settings.Country != "DE" {
		t.Fatalf("expected country DE, got %s", settings.Country)
	}
	if settings.MTProtoSecret == "" {
		t.Fatal("expected non-empty mtproto_secret")
	}
	if settings.ProtocolSalt == "" {
		t.Fatal("expected non-empty protocol_salt")
	}
	if settings.TUICPassword == "" || len(settings.TUICPassword) < 16 {
		t.Fatal("expected strong tuic_password")
	}

	// Idempotency check
	settingsSecond, err := Ensure(context.Background(), store, appCfg, opts)
	if err != nil {
		t.Fatalf("Ensure second run failed: %v", err)
	}
	if settingsSecond.MTProtoSecret != settings.MTProtoSecret {
		t.Fatalf("mtproto_secret changed on second run: %s != %s", settingsSecond.MTProtoSecret, settings.MTProtoSecret)
	}
	if settingsSecond.ProtocolSalt != settings.ProtocolSalt {
		t.Fatalf("protocol_salt changed on second run")
	}
}
