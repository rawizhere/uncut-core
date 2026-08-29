package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rawizhere/uncut-core/internal/db"
)

func TestClientLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clients_test_*")
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

	// Invalid name
	if err := ValidateClientName("Invalid User!"); err == nil {
		t.Errorf("Expected error for invalid client name")
	}

	// Valid creation
	client, err := AddClient(store, "john_doe", []string{"vless-reality", "tuic"})
	if err != nil {
		t.Fatalf("AddClient failed: %v", err)
	}

	if client.Name != "john_doe" {
		t.Errorf("Unexpected name: %s", client.Name)
	}
	if client.UUID == "" {
		t.Errorf("Expected non-empty UUID")
	}
	if client.Password == "" {
		t.Errorf("Expected non-empty password")
	}
	if client.SubHash == "" {
		t.Errorf("Expected non-empty SubHash")
	}
	if len(client.Protocols) != 2 {
		t.Errorf("Expected 2 protocols, got %d", len(client.Protocols))
	}

	// Duplicate creation
	if _, err := AddClient(store, "john_doe", nil); !errors.Is(err, ErrClientExists) {
		t.Errorf("Expected ErrClientExists, got %v", err)
	}

	// Update protocols
	updated, err := UpdateClientProtocols(store, client.UUID, []string{"vless-ws"})
	if err != nil {
		t.Fatalf("UpdateClientProtocols failed: %v", err)
	}
	if len(updated.Protocols) != 1 || updated.Protocols[0] != "vless-ws" {
		t.Errorf("Expected updated protocols [vless-ws], got %v", updated.Protocols)
	}

	// List
	list, err := store.GetClients()
	if err != nil {
		t.Fatalf("GetClients failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("Expected 1 client, got %d", len(list))
	}

	// Delete drops the subscription file along with the record
	subsDir := filepath.Join(tmpDir, "subs")
	if err := os.MkdirAll(subsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	subFile := filepath.Join(subsDir, client.SubHash)
	if err := os.WriteFile(subFile, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := DeleteClient(store, subsDir, client.UUID); err != nil {
		t.Fatalf("DeleteClient failed: %v", err)
	}
	if _, err := os.Stat(subFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Subscription file left behind after delete: %v", err)
	}

	listAfter, err := store.GetClients()
	if err != nil {
		t.Fatalf("GetClients after delete failed: %v", err)
	}
	if len(listAfter) != 0 {
		t.Errorf("Expected 0 clients, got %d", len(listAfter))
	}
}
