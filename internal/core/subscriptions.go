package core

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
)

func getDPILinkParams(s config.Settings) string {
	var params strings.Builder
	if s.DPIFragment == "true" {
		params.WriteString("&fragment=10-500,0-20")
	}
	if s.DPIPadding == "true" {
		params.WriteString("&padding=900-1200")
	}
	return params.String()
}

func getRegion(s config.Settings) string {
	if s.Region != "" {
		return s.Region
	}
	if strings.Contains(s.Domain, "eu-1") {
		return "eu-1"
	}
	if strings.Contains(s.Domain, "eu-2") {
		return "eu-2"
	}
	if strings.Contains(s.Domain, "ap-1") {
		return "ap-1"
	}
	return "eu-1"
}

func GenerateVLESSRealityLink(client config.Client, s config.Settings) string {
	sni := s.SNI
	if sni == "" {
		sni = "dl.google.com"
	}
	dpiParams := getDPILinkParams(s)
	region := getRegion(s)

	return fmt.Sprintf(
		"vless://%s@%s:8443?type=tcp&encryption=none&security=reality&pbk=%s&fp=chrome&sni=%s&sid=%s&spx=%%2F&flow=xtls-rprx-vision%s#%s-%s-01",
		client.UUID, s.Domain, s.RealityPubKey, sni, s.RealityShortID, dpiParams, client.Name, region,
	)
}

func GenerateTUICLink(client config.Client, s config.Settings) string {
	port := s.TUICPort
	if port == "" {
		port = "443"
	}
	pass := client.Password
	if pass == "" {
		pass = client.UUID
	}
	region := getRegion(s)

	return fmt.Sprintf(
		"tuic://%s:%s@%s:%s?congestion_control=bbr&udp_relay_mode=native&alpn=h3&allow_insecure=0#%s-%s-02",
		client.UUID, pass, s.Domain, port, client.Name, region,
	)
}

func GenerateVLESSWSLink(client config.Client, s config.Settings) string {
	salt := s.ProtocolSalt
	if salt == "" {
		salt = "default"
	}
	path := fmt.Sprintf("/v1/streams/live-%s/ws", salt)
	encodedPath := url.PathEscape(path)
	dpiParams := getDPILinkParams(s)
	region := getRegion(s)

	return fmt.Sprintf(
		"vless://%s@%s:443?type=ws&security=tls&path=%s&encryption=none&fp=chrome%s#%s-%s-03",
		client.UUID, s.Domain, encodedPath, dpiParams, client.Name, region,
	)
}

func GenerateXHTTPStealthLink(client config.Client, s config.Settings) string {
	salt := s.ProtocolSalt
	if salt == "" {
		salt = "default"
	}
	path := fmt.Sprintf("/v1/ingest/push/live-%s", salt)
	encodedPath := url.PathEscape(path)
	dpiParams := getDPILinkParams(s)
	region := getRegion(s)

	return fmt.Sprintf(
		"vless://%s@%s:443?type=xhttp&security=tls&path=%s&encryption=none&mode=stream-up&host=%s&fp=chrome%s#%s-%s-04",
		client.UUID, s.Domain, encodedPath, s.Domain, dpiParams, client.Name, region,
	)
}

func GenerateVLESSHTTPUpgradeLink(client config.Client, s config.Settings) string {
	salt := s.ProtocolSalt
	if salt == "" {
		salt = "default"
	}
	path := fmt.Sprintf("/v1/streams/live-%s/upgrade", salt)
	encodedPath := url.PathEscape(path)
	dpiParams := getDPILinkParams(s)
	region := getRegion(s)

	return fmt.Sprintf(
		"vless://%s@%s:443?type=httpupgrade&security=tls&path=%s&encryption=none&host=%s&fp=chrome%s#%s-%s-05",
		client.UUID, s.Domain, encodedPath, s.Domain, dpiParams, client.Name, region,
	)
}

func GenerateVLESSGRPCLink(client config.Client, s config.Settings) string {
	dpiParams := getDPILinkParams(s)
	region := getRegion(s)

	return fmt.Sprintf(
		"vless://%s@%s:443?type=grpc&security=tls&serviceName=ingest.v1.IngestService&encryption=none&host=%s&fp=chrome%s#%s-%s-06",
		client.UUID, s.Domain, s.Domain, dpiParams, client.Name, region,
	)
}

func GenerateClientLinks(client config.Client, s config.Settings) []string {
	activeServerProtos := s.Protocols
	if len(activeServerProtos) == 0 {
		activeServerProtos = make([]string, len(config.DefaultProtocols))
		for i, p := range config.DefaultProtocols {
			activeServerProtos[i] = string(p)
		}
	}

	serverProtoMap := make(map[string]bool)
	for _, p := range activeServerProtos {
		serverProtoMap[strings.TrimSpace(p)] = true
	}

	clientProtos := client.Protocols
	if len(clientProtos) == 0 {
		clientProtos = activeServerProtos
	}

	links := make([]string, 0)
	for _, proto := range clientProtos {
		proto = strings.TrimSpace(proto)
		if !serverProtoMap[proto] {
			continue
		}

		switch proto {
		case string(config.ProtoVLESSReality):
			links = append(links, GenerateVLESSRealityLink(client, s))
		case string(config.ProtoTUIC):
			links = append(links, GenerateTUICLink(client, s))
		case string(config.ProtoVLESSWS):
			links = append(links, GenerateVLESSWSLink(client, s))
		case string(config.ProtoXHTTPStealth):
			links = append(links, GenerateXHTTPStealthLink(client, s))
		case string(config.ProtoVLESSHTTPUpgrade):
			links = append(links, GenerateVLESSHTTPUpgradeLink(client, s))
		case string(config.ProtoVLESSGRPC):
			links = append(links, GenerateVLESSGRPCLink(client, s))
		}
	}

	return links
}

func GenerateSubscriptionPayload(client config.Client, s config.Settings) string {
	links := GenerateClientLinks(client, s)
	joined := strings.Join(links, "\n")
	if len(joined) > 0 {
		joined += "\n"
	}
	return base64.StdEncoding.EncodeToString([]byte(joined))
}

func GetSubscriptionURL(domain, subHash string) string {
	return fmt.Sprintf("https://%s/v1/schemas/%s.bin", domain, subHash)
}

func WriteSubscriptionFile(subDir string, client config.Client, s config.Settings) error {
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return fmt.Errorf("create subscription dir: %w", err)
	}

	payload := GenerateSubscriptionPayload(client, s)
	filePath := filepath.Join(subDir, client.SubHash)

	if err := os.WriteFile(filePath, []byte(payload), 0644); err != nil {
		return fmt.Errorf("write subscription file: %w", err)
	}

	return nil
}

func RegenerateAllSubscriptions(store *db.Store, subDir string, s config.Settings) error {
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return fmt.Errorf("create subscription dir: %w", err)
	}

	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("fetch clients: %w", err)
	}

	for _, client := range clients {
		if client.SubHash == "" {
			client.SubHash = GenerateSubHash(client.UUID, s.SubSalt)
			if err := store.AddClient(client); err != nil {
				return fmt.Errorf("persist sub hash for %s: %w", client.Name, err)
			}
		}
		if err := WriteSubscriptionFile(subDir, client, s); err != nil {
			slog.Error("Failed to write subscription file", "name", client.Name, "err", err)
			return err
		}
	}

	slog.Info("Regenerated all subscription files", "count", len(clients), "dir", subDir)
	return nil
}
