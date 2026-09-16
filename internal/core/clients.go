package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
)

var clientNameRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)

var (
	ErrInvalidClientName = errors.New("invalid client name: must match ^[a-z0-9_-]+$")
	ErrClientExists      = errors.New("client with this name already exists")
	ErrClientNotFound    = errors.New("client not found")
)

func ValidateClientName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || !clientNameRegex.MatchString(name) {
		return ErrInvalidClientName
	}
	return nil
}

func GenerateUUID() string {
	return uuid.NewString()
}

func GeneratePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// GenerateSubToken makes the per-client subscription secret: random, so one rotation kills a leaked URL.
func GenerateSubToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// DefaultProtocols must mirror config.DefaultProtocols: lag makes subscriptions omit protocols.
func DefaultProtocols() []string {
	return []string{
		string(config.ProtoXHTTPStealth),
		string(config.ProtoVLESSWS),
		string(config.ProtoVLESSHTTPUpgrade),
		string(config.ProtoVLESSGRPC),
		string(config.ProtoVLESSReality),
		string(config.ProtoTUIC),
	}
}

func AddClientWithDefaultProtocols(store *db.Store, name string) (*config.Client, error) {
	return AddClient(store, name, DefaultProtocols())
}

func AddClient(store *db.Store, name string, protocols []string) (*config.Client, error) {
	if err := ValidateClientName(name); err != nil {
		return nil, err
	}

	clients, err := store.GetClients()
	if err != nil {
		return nil, fmt.Errorf("fetch clients: %w", err)
	}

	for _, c := range clients {
		if c.Name == name {
			return nil, ErrClientExists
		}
	}

	clientUUID := GenerateUUID()

	sanitizedProtos := make([]string, 0, len(protocols))
	for _, p := range protocols {
		p = strings.TrimSpace(p)
		if p != "" {
			sanitizedProtos = append(sanitizedProtos, p)
		}
	}

	client := config.Client{
		UUID:      clientUUID,
		Name:      name,
		Password:  GeneratePassword(),
		SubHash:   GenerateSubToken(),
		Protocols: sanitizedProtos,
		CreatedAt: config.GetMSKTime(),
	}

	if err := store.AddClient(client); err != nil {
		return nil, fmt.Errorf("save client: %w", err)
	}

	slog.Info("Client created", "name", name, "uuid", clientUUID)
	return &client, nil
}

// RotateClientSub replaces the per-client subscription token: the old URL dies, the file under the old hash is removed.
func RotateClientSub(store *db.Store, subsDir, clientUUID string) (*config.Client, error) {
	client, err := store.GetClientByUUID(clientUUID)
	if err != nil {
		return nil, fmt.Errorf("client not found: %w", err)
	}

	old := client.SubHash
	client.SubHash = GenerateSubToken()
	if err := store.AddClient(*client); err != nil {
		return nil, fmt.Errorf("rotate sub token: %w", err)
	}

	if subsDir != "" && old != "" {
		if err := os.Remove(filepath.Join(subsDir, old)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Failed to remove old subscription file", "uuid", clientUUID, "error", err)
		}
	}

	slog.Info("Client subscription rotated", "name", client.Name, "uuid", clientUUID)
	return client, nil
}

func DeleteClient(store *db.Store, subsDir, clientUUID string) error {
	client, err := store.GetClientByUUID(clientUUID)
	if err != nil {
		return fmt.Errorf("client not found: %w", err)
	}

	if err := store.DeleteClient(clientUUID); err != nil {
		return fmt.Errorf("delete client: %w", err)
	}

	if subsDir != "" && client.SubHash != "" {
		if err := os.Remove(filepath.Join(subsDir, client.SubHash)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Failed to remove subscription file", "uuid", clientUUID, "error", err)
		}
	}

	slog.Info("Client deleted", "name", client.Name, "uuid", clientUUID)
	return nil
}

func UpdateClientProtocols(store *db.Store, clientUUID string, protocols []string) (*config.Client, error) {
	client, err := store.GetClientByUUID(clientUUID)
	if err != nil {
		return nil, fmt.Errorf("client not found: %w", err)
	}

	sanitizedProtos := make([]string, 0, len(protocols))
	for _, p := range protocols {
		p = strings.TrimSpace(p)
		if p != "" {
			sanitizedProtos = append(sanitizedProtos, p)
		}
	}

	client.Protocols = sanitizedProtos
	client.ProtocolsExplicit = true
	if err := store.AddClient(*client); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}

	slog.Info("Client protocols updated", "name", client.Name, "uuid", clientUUID, "protocols", sanitizedProtos)
	return client, nil
}

// InheritClientProtocols resets to snapshot semantics: the stored list clears, the server's set flows.
func InheritClientProtocols(store *db.Store, clientUUID string) (*config.Client, error) {
	client, err := store.GetClientByUUID(clientUUID)
	if err != nil {
		return nil, fmt.Errorf("client not found: %w", err)
	}
	client.Protocols = nil
	client.ProtocolsExplicit = false
	if err := store.AddClient(*client); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}
	slog.Info("Client protocols reset to inherit", "name", client.Name, "uuid", clientUUID)
	return client, nil
}
