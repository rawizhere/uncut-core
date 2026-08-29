package core

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
)

func TestGenerateClientLinks(t *testing.T) {
	settings := config.Settings{
		Domain:         "vpn.example.com",
		Country:        "SE",
		RealityPubKey:  "pubkey123",
		RealityShortID: "sid123",
		SNI:            "dl.google.com",
		ProtocolSalt:   "salt456",
		TUICPort:       "443",
		Protocols: []string{
			string(config.ProtoVLESSReality),
			string(config.ProtoTUIC),
			string(config.ProtoVLESSWS),
			string(config.ProtoXHTTPStealth),
			string(config.ProtoVLESSHTTPUpgrade),
			string(config.ProtoVLESSGRPC),
		},
	}

	client := config.Client{
		UUID:      "abcd-1234-uuid",
		Name:      "alice",
		Password:  "passSecret",
		Protocols: []string{}, // all active protocols
	}

	links := GenerateClientLinks(client, settings)
	if len(links) != 6 {
		t.Fatalf("Expected 6 links, got %d", len(links))
	}

	// Test VLESS Reality
	if !strings.HasPrefix(links[0], "vless://abcd-1234-uuid@vpn.example.com:8443") {
		t.Errorf("Unexpected reality link: %s", links[0])
	}
	if !strings.Contains(links[0], "#alice-vless-reality-SE") {
		t.Errorf("Missing hash tag in reality link: %s", links[0])
	}

	// Test TUIC
	if !strings.HasPrefix(links[1], "tuic://abcd-1234-uuid:passSecret@vpn.example.com:443") {
		t.Errorf("Unexpected tuic link: %s", links[1])
	}
	if !strings.Contains(links[1], "#alice-tuic-SE") {
		t.Errorf("Missing hash tag in tuic link: %s", links[1])
	}

	// Test VLESS WS
	if !strings.Contains(links[2], "path=%2Fassets%2Fcss%2Fsalt456") && !strings.Contains(links[2], "path=/assets/css/salt456") {
		t.Errorf("Unexpected ws path in link: %s", links[2])
	}

	// Test XHTTP Stealth
	if !strings.Contains(links[3], "mode=stream-up") {
		t.Errorf("Missing mode in xhttp link: %s", links[3])
	}

	// Test VLESS gRPC
	if !strings.Contains(links[5], "serviceName=EdgeContent_salt456") {
		t.Errorf("Missing serviceName in grpc link: %s", links[5])
	}
}

func TestGenerateSubscriptionPayload(t *testing.T) {
	settings := config.Settings{
		Domain:       "vpn.example.com",
		Country:      "US",
		ProtocolSalt: "salt456",
		Protocols:    []string{string(config.ProtoVLESSWS)},
	}

	client := config.Client{
		UUID:      "abcd-1234-uuid",
		Name:      "bob",
		Protocols: []string{"vless-ws"},
	}

	payload := GenerateSubscriptionPayload(client, settings)
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("Failed to decode base64 payload: %v", err)
	}

	if !strings.Contains(string(decoded), "vless://abcd-1234-uuid@vpn.example.com:443") {
		t.Errorf("Decoded content does not contain expected link: %s", string(decoded))
	}
}

func TestWriteAndRegenerateSubscriptions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subs_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer func() { _ = store.Close() }()

	client, err := AddClient(store, "testuser", []string{"vless-ws"})
	if err != nil {
		t.Fatalf("AddClient failed: %v", err)
	}

	subDir := filepath.Join(tmpDir, "subs")
	settings := config.Settings{
		Domain:       "test.example.com",
		ProtocolSalt: "salt123",
		Protocols:    []string{"vless-ws"},
	}

	if err := RegenerateAllSubscriptions(store, subDir, settings); err != nil {
		t.Fatalf("RegenerateAllSubscriptions failed: %v", err)
	}

	subFile := filepath.Join(subDir, client.SubHash)
	data, err := os.ReadFile(subFile)
	if err != nil {
		t.Fatalf("Subscription file not created: %v", err)
	}

	if len(data) == 0 {
		t.Errorf("Subscription file is empty")
	}
}
