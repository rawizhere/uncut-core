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

// linkLabel builds the visible entry name: name-protocol[-tag].
// Lives only in the URL fragment — clients never send it to the server.
func linkLabel(name, proto, tag string) string {
	label := fmt.Sprintf("%s-%s", name, proto)
	if IsValidTag(tag) {
		label += "-" + tag
	}
	return label
}

// IsValidTag: 1-16 alnum/dash/underscore, safe in a URL fragment; anything else renders no tag.
func IsValidTag(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func GenerateVLESSRealityLink(client config.Client, s config.Settings) string {
	// Link SNI must match the stream splitter's map; the transport is its source.
	sni := s.RealityServerName
	// 8443 is the loopback reality inbound; 443 is shared with nginx by SNI split.
	return fmt.Sprintf(
		"vless://%s@%s:443?type=tcp&encryption=none&security=reality&pbk=%s&fp=chrome&sni=%s&sid=%s&spx=%%2F&flow=xtls-rprx-vision#%s",
		client.UUID, s.Domain, s.RealityPubKey, sni, s.RealityShortID, linkLabel(client.Name, "reality", s.Tag),
	)
}

func GenerateTUICLink(client config.Client, s config.Settings) string {
	port := s.TUICPort
	if port == "" {
		port = config.DefaultTUICPort
	}
	pass := client.Password
	if pass == "" {
		pass = client.UUID
	}
	return fmt.Sprintf(
		"tuic://%s:%s@%s:%s?congestion_control=bbr&udp_relay_mode=native&alpn=h3&allow_insecure=0#%s",
		client.UUID, pass, s.Domain, port, linkLabel(client.Name, "tuic", s.Tag),
	)
}

func GenerateVLESSWSLink(client config.Client, s config.Settings) string {
	path := s.Transport.WSPath()
	encodedPath := url.PathEscape(path)
	// ed=2048: client may pack the first bytes into the WebSocket handshake.
	return fmt.Sprintf(
		"vless://%s@%s:443?type=ws&security=tls&path=%s&encryption=none&ed=2048&fp=chrome#%s",
		client.UUID, s.Domain, encodedPath, linkLabel(client.Name, "ws", s.Tag),
	)
}

func GenerateXHTTPStealthLink(client config.Client, s config.Settings) string {
	path := s.Transport.XHTTPPath()
	encodedPath := url.PathEscape(path)
	return fmt.Sprintf(
		"vless://%s@%s:443?type=xhttp&security=tls&path=%s&encryption=none&mode=packet-up&host=%s&fp=chrome#%s",
		client.UUID, s.Domain, encodedPath, s.Domain, linkLabel(client.Name, "xhttp", s.Tag),
	)
}

func GenerateVLESSHTTPUpgradeLink(client config.Client, s config.Settings) string {
	path := s.Transport.UpgradePath()
	encodedPath := url.PathEscape(path)
	return fmt.Sprintf(
		"vless://%s@%s:443?type=httpupgrade&security=tls&path=%s&encryption=none&host=%s&fp=chrome#%s",
		client.UUID, s.Domain, encodedPath, s.Domain, linkLabel(client.Name, "httpupgrade", s.Tag),
	)
}

func GenerateVLESSGRPCLink(client config.Client, s config.Settings) string {
	return fmt.Sprintf(
		"vless://%s@%s:443?type=grpc&security=tls&serviceName=%s&encryption=none&host=%s&fp=chrome#%s",
		client.UUID, s.Domain, s.Transport.GRPCService(), s.Domain, linkLabel(client.Name, "grpc", s.Tag),
	)
}

func GenerateClientLinks(client config.Client, s config.Settings) []string {
	activeServerProtos := config.ResolveProtocols(s.Protocols)

	serverProtoMap := make(map[string]bool)
	for _, p := range activeServerProtos {
		serverProtoMap[strings.TrimSpace(p)] = true
	}

	clientProtos := append([]string(nil), client.Protocols...)
	if !client.ProtocolsExplicit {
		// Non-explicit lists are creation snapshots: they merge in new server
		// protocols. Explicit is an allowlist — off stays off.
		for _, sp := range activeServerProtos {
			sp = strings.TrimSpace(sp)
			if sp == "" {
				continue
			}
			found := false
			for _, cp := range clientProtos {
				if strings.TrimSpace(cp) == sp {
					found = true
					break
				}
			}
			if !found {
				clientProtos = append(clientProtos, sp)
			}
		}
	}
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
			client.SubHash = GenerateSubToken()
			if err := store.AddClient(client); err != nil {
				return fmt.Errorf("persist sub token for %s: %w", client.Name, err)
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
