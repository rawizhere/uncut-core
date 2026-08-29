package acme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSigned(t *testing.T) {
	tmpDir := t.TempDir()
	certsDir := filepath.Join(tmpDir, "certs")
	webRoot := filepath.Join(tmpDir, "webroot")

	mgr := New("test.example.com", "test@example.com", certsDir, webRoot, true, "", "")

	issued, err := mgr.EnsureSelfSigned()
	if err != nil {
		t.Fatalf("EnsureSelfSigned failed: %v", err)
	}
	if !issued {
		t.Fatal("expected cert to be issued on first run")
	}

	if _, err := os.Stat(mgr.CertPath()); err != nil {
		t.Fatalf("cert file missing: %v", err)
	}
	if _, err := os.Stat(mgr.KeyPath()); err != nil {
		t.Fatalf("key file missing: %v", err)
	}

	// Second run should be a no-op
	issuedAgain, err := mgr.EnsureSelfSigned()
	if err != nil {
		t.Fatalf("EnsureSelfSigned second run failed: %v", err)
	}
	if issuedAgain {
		t.Fatal("expected no-op on second run")
	}
}
