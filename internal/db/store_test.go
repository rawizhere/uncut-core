package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rawizhere/uncut-core/internal/config"
)

func TestStoreOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.SetSetting("domain", "example.com"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}

	val, err := store.GetSetting("domain")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if val != "example.com" {
		t.Fatalf("expected 'example.com', got '%s'", val)
	}

	if err := store.SetSetting("domain", "updated.com"); err != nil {
		t.Fatalf("SetSetting update failed: %v", err)
	}
	val, err = store.GetSetting("domain")
	if err != nil || val != "updated.com" {
		t.Fatalf("expected updated value 'updated.com', got '%s', err: %v", val, err)
	}

	client := config.Client{
		UUID:      "test-uuid-1",
		Name:      "alice",
		Password:  "secret123",
		SubHash:   "hash123",
		Protocols: []string{string(config.ProtoVLESSReality), string(config.ProtoTUIC)},
		CreatedAt: time.Now(),
	}

	if err := store.AddClient(client); err != nil {
		t.Fatalf("AddClient failed: %v", err)
	}

	clients, err := store.GetClients()
	if err != nil {
		t.Fatalf("GetClients failed: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(clients))
	}
	if clients[0].UUID != client.UUID || len(clients[0].Protocols) != 2 {
		t.Fatalf("client data mismatch: %+v", clients[0])
	}

	byUUID, err := store.GetClientByUUID(client.UUID)
	if err != nil {
		t.Fatalf("GetClientByUUID failed: %v", err)
	}
	if byUUID == nil || byUUID.Name != client.Name {
		t.Fatalf("unexpected client by UUID: %+v", byUUID)
	}

	if err := store.DeleteClient(client.UUID); err != nil {
		t.Fatalf("DeleteClient failed: %v", err)
	}

	clientsAfterDelete, err := store.GetClients()
	if err != nil {
		t.Fatalf("GetClients after delete failed: %v", err)
	}
	if len(clientsAfterDelete) != 0 {
		t.Fatalf("expected 0 clients after delete, got %d", len(clientsAfterDelete))
	}
}
