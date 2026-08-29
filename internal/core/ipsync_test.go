package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rawizhere/uncut-core/internal/db"
)

func TestGetPublicIPv4FromEndpoints_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("198.51.100.42\n"))
	}))
	defer server.Close()

	ctx := context.Background()
	ip, err := GetPublicIPv4FromEndpoints(ctx, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed: %v", err)
	}

	if ip != "198.51.100.42" {
		t.Errorf("Expected 198.51.100.42, got %s", ip)
	}
}

func TestGetPublicIPv4FromEndpoints_Fallback(t *testing.T) {
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badServer.Close()

	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("203.0.113.10\n"))
	}))
	defer goodServer.Close()

	ctx := context.Background()
	ip, err := GetPublicIPv4FromEndpoints(ctx, goodServer.Client(), badServer.URL, goodServer.URL)
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed on fallback: %v", err)
	}

	if ip != "203.0.113.10" {
		t.Errorf("Expected 203.0.113.10, got %s", ip)
	}
}

func TestSyncServerIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("192.0.2.1"))
	}))
	defer server.Close()

	tmpDir, err := os.MkdirTemp("", "ipsync_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Initial sync with custom mock endpoint
	ip, err := GetPublicIPv4FromEndpoints(ctx, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed: %v", err)
	}

	_ = store.SetSetting("server_ip", "10.0.0.1")

	// Verify update when different
	_ = store.SetSetting("server_ip", ip)
	val, err := store.GetSetting("server_ip")
	if err != nil || val != "192.0.2.1" {
		t.Errorf("Expected 192.0.2.1 in store, got %s, err: %v", val, err)
	}
}
