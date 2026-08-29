package singbox

import (
	"encoding/json"
	"testing"

	"github.com/rawizhere/uncut-core/internal/config"
)

func TestGenerateConfig(t *testing.T) {
	settings := config.Settings{
		Domain:         "test.example.com",
		RealityPrivKey: "mock_priv_key",
		RealityPubKey:  "mock_pub_key",
		RealityShortID: "abcd12",
		SNI:            "dl.google.com",
		ProtocolSalt:   "a1b2c3d4",
		TUICPort:       "443",
		Protocols: []string{
			string(config.ProtoXHTTPStealth),
			string(config.ProtoVLESSWS),
			string(config.ProtoVLESSHTTPUpgrade),
			string(config.ProtoVLESSGRPC),
			string(config.ProtoVLESSReality),
			string(config.ProtoTUIC),
		},
	}

	clients := []config.Client{
		{
			UUID:      "11111111-1111-1111-1111-111111111111",
			Name:      "user1",
			Password:  "pass1234",
			Protocols: []string{"vless-reality", "tuic", "xhttp-stealth"},
		},
		{
			UUID:      "22222222-2222-2222-2222-222222222222",
			Name:      "user2",
			Password:  "pass5678",
			Protocols: []string{}, // all protocols
		},
	}

	data, err := GenerateConfig(settings, clients)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse generated json: %v", err)
	}

	if len(parsed.Inbounds) != 6 {
		t.Fatalf("Expected 6 inbounds, got %d", len(parsed.Inbounds))
	}

	inboundTags := make(map[string]map[string]any)
	for _, in := range parsed.Inbounds {
		tag, _ := in["tag"].(string)
		inboundTags[tag] = in
	}

	// Verify xhttp-stealth
	if xhttp, ok := inboundTags["xhttp-stealth"]; !ok {
		t.Errorf("Missing xhttp-stealth inbound")
	} else {
		transport, _ := xhttp["transport"].(map[string]any)
		if transport["path"] != "/assets/js/a1b2c3d4" {
			t.Errorf("Unexpected xhttp path: %v", transport["path"])
		}
	}

	// Verify vless-ws
	if ws, ok := inboundTags["vless-ws"]; !ok {
		t.Errorf("Missing vless-ws inbound")
	} else {
		transport, _ := ws["transport"].(map[string]any)
		if transport["path"] != "/assets/css/a1b2c3d4" {
			t.Errorf("Unexpected ws path: %v", transport["path"])
		}
	}

	// Verify vless-grpc
	if grpc, ok := inboundTags["vless-grpc"]; !ok {
		t.Errorf("Missing vless-grpc inbound")
	} else {
		transport, _ := grpc["transport"].(map[string]any)
		if transport["service_name"] != "EdgeContent_a1b2c3d4" {
			t.Errorf("Unexpected grpc service_name: %v", transport["service_name"])
		}
	}

	// Verify vless-reality
	if reality, ok := inboundTags["vless-reality"]; !ok {
		t.Errorf("Missing vless-reality inbound")
	} else {
		users, _ := reality["users"].([]any)
		if len(users) != 2 {
			t.Errorf("Expected 2 reality users, got %d", len(users))
		}
	}

	// Verify tuic
	if tuic, ok := inboundTags["tuic"]; !ok {
		t.Errorf("Missing tuic inbound")
	} else {
		users, _ := tuic["users"].([]any)
		if len(users) != 2 {
			t.Errorf("Expected 2 tuic users, got %d", len(users))
		}
	}
}

func TestGenerateConfig_SelectiveProtocols(t *testing.T) {
	settings := config.Settings{
		Domain:       "test.example.com",
		ProtocolSalt: "salt123",
		Protocols:    []string{string(config.ProtoVLESSWS)},
	}

	clients := []config.Client{
		{
			UUID: "11111111-1111-1111-1111-111111111111",
			Name: "user1",
		},
	}

	data, err := GenerateConfig(settings, clients)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}

	if len(parsed.Inbounds) != 1 {
		t.Fatalf("Expected 1 inbound, got %d", len(parsed.Inbounds))
	}
	if parsed.Inbounds[0]["tag"] != "vless-ws" {
		t.Fatalf("Expected inbound tag vless-ws, got %v", parsed.Inbounds[0]["tag"])
	}
}
