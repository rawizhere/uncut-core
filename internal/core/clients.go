package core

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
)

const legacySubSalt = "uncut-core-sub-salt-v1"

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

func GenerateSubHash(clientUUID, salt string) string {
	if salt == "" {
		salt = legacySubSalt
	}
	sum := md5.Sum([]byte(clientUUID + salt))
	return hex.EncodeToString(sum[:])
}

func subSalt(store *db.Store) string {
	value, err := store.GetSetting("sub_salt")
	if err != nil || value == "" {
		return legacySubSalt
	}
	return value
}

func AddClientWithDefaultProtocols(store *db.Store, name string) (*config.Client, error) {
	defaultProtos := []string{
		string(config.ProtoXHTTPStealth),
		string(config.ProtoVLESSWS),
		string(config.ProtoVLESSHTTPUpgrade),
		string(config.ProtoVLESSGRPC),
		string(config.ProtoVLESSReality),
		string(config.ProtoTUIC),
	}
	return AddClient(store, name, defaultProtos)
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
	password := GeneratePassword()
	subHash := GenerateSubHash(clientUUID, subSalt(store))

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
		Password:  password,
		SubHash:   subHash,
		Protocols: sanitizedProtos,
		CreatedAt: config.GetMSKTime(),
	}

	if err := store.AddClient(client); err != nil {
		return nil, fmt.Errorf("save client: %w", err)
	}

	slog.Info("Client created", "name", name, "uuid", clientUUID, "sub_hash", subHash)
	return &client, nil
}

func DeleteClient(store *db.Store, clientUUID string) error {
	client, err := store.GetClientByUUID(clientUUID)
	if err != nil {
		return fmt.Errorf("client not found: %w", err)
	}

	if err := store.DeleteClient(clientUUID); err != nil {
		return fmt.Errorf("delete client: %w", err)
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
	if err := store.AddClient(*client); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}

	slog.Info("Client protocols updated", "name", client.Name, "uuid", clientUUID, "protocols", sanitizedProtos)
	return client, nil
}

func GetClient(store *db.Store, clientUUID string) (*config.Client, error) {
	return store.GetClientByUUID(clientUUID)
}

func ListClients(store *db.Store) ([]config.Client, error) {
	return store.GetClients()
}
