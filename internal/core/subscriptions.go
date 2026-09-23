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

// linkLabel builds the visible entry name: name-protocol[-tag]. Lives only in the URL fragment — clients never send it to the server.
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

func GenerateHysteria2Link(client config.Client, s config.Settings) string {
	pass := client.Password
	if pass == "" {
		pass = client.UUID
	}
	link := fmt.Sprintf("hy2://%s@%s:443", pass, s.Domain)
	// Obfs only when the node generated a password; params must match the inbound or the client hears silence.
	if s.Hysteria2Obfs != "" {
		link += "?obfs=salamander&obfs-password=" + s.Hysteria2Obfs
	}
	// mport only when the host has the DNAT range; a closed range would kill the whole link.
	if s.Hysteria2HopPorts != "" {
		sep := "?"
		if strings.Contains(link, "?") {
			sep = "&"
		}
		link += sep + "mport=" + s.Hysteria2HopPorts
	}
	return link + "#" + linkLabel(client.Name, "hysteria2", s.Tag)
}

func GenerateVLESSWSLink(client config.Client, s config.Settings) string {
	path := s.Transport.WSPath()
	encodedPath := url.PathEscape(path)
	// No ed: some converters drop early_data_header_name and build a dead ws outbound.
	return fmt.Sprintf(
		"vless://%s@%s:443?type=ws&security=tls&path=%s&encryption=none&fp=chrome#%s",
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

	// Stored client rows predate the hysteria2 swap: normalize before matching, and rows may hold both the retired name and the replacement.
	seen := make(map[string]bool, len(client.Protocols))
	clientProtos := make([]string, 0, len(client.Protocols))
	for _, cp := range client.Protocols {
		if strings.TrimSpace(cp) == "tuic" {
			cp = string(config.ProtoHysteria2)
		}
		if seen[cp] {
			continue
		}
		seen[cp] = true
		clientProtos = append(clientProtos, cp)
	}
	if !client.ProtocolsExplicit {
		// Non-explicit lists are creation snapshots: they merge in new server protocols. Explicit is an allowlist — off stays off.
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
		case string(config.ProtoHysteria2):
			links = append(links, GenerateHysteria2Link(client, s))
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
