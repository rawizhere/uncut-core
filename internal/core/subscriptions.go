package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
		"vless://%s@%s:443?type=tcp&encryption=none&security=reality&pbk=%s&fp=%s&sni=%s&sid=%s&spx=%%2F&flow=xtls-rprx-vision#%s",
		client.UUID, s.Domain, s.RealityPubKey, s.Fingerprint, sni, s.RealityShortID, linkLabel(client.Name, "reality", s.Tag),
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
	// hy2 URI spec wants a dash in the range (mport=20000-30000); the colon form is sing-box-server only.
	if s.Hysteria2HopPorts != "" {
		sep := "?"
		if strings.Contains(link, "?") {
			sep = "&"
		}
		link += sep + "mport=" + strings.ReplaceAll(s.Hysteria2HopPorts, ":", "-")
	}
	return link + "#" + linkLabel(client.Name, "hysteria2", s.Tag)
}

func GenerateVLESSWSLink(client config.Client, s config.Settings) string {
	path := s.Transport.WSPath()
	encodedPath := url.PathEscape(path)
	// No ed: some converters drop early_data_header_name and build a dead ws outbound.
	return fmt.Sprintf(
		"vless://%s@%s:443?type=ws&security=tls&path=%s&encryption=none&fp=%s#%s",
		client.UUID, s.Domain, encodedPath, s.Fingerprint, linkLabel(client.Name, "ws", s.Tag),
	)
}

// xhttpExtra carries the client-side burst shaping: xmux keeps one long-lived
// connection per host instead of a fan of fresh handshakes per request burst.
func xhttpExtra() string {
	extra, _ := json.Marshal(map[string]any{
		"xmux": map[string]string{
			"maxConcurrency":   "16-32",
			"hMaxReusableSecs": "300",
		},
		"xPaddingBytes": "100-2500",
	})
	return base64.RawURLEncoding.EncodeToString(extra)
}

func GenerateXHTTPStealthLink(client config.Client, s config.Settings) string {
	path := s.Transport.XHTTPPath()
	encodedPath := url.PathEscape(path)
	return fmt.Sprintf(
		"vless://%s@%s:443?type=xhttp&security=tls&path=%s&encryption=none&mode=packet-up&host=%s&fp=%s&extra=%s#%s",
		client.UUID, s.Domain, encodedPath, s.Domain, s.Fingerprint, xhttpExtra(), linkLabel(client.Name, "xhttp", s.Tag),
	)
}

func GenerateVLESSHTTPUpgradeLink(client config.Client, s config.Settings) string {
	path := s.Transport.UpgradePath()
	encodedPath := url.PathEscape(path)
	return fmt.Sprintf(
		"vless://%s@%s:443?type=httpupgrade&security=tls&path=%s&encryption=none&host=%s&fp=%s#%s",
		client.UUID, s.Domain, encodedPath, s.Domain, s.Fingerprint, linkLabel(client.Name, "httpupgrade", s.Tag),
	)
}

func GenerateVLESSGRPCLink(client config.Client, s config.Settings) string {
	return fmt.Sprintf(
		"vless://%s@%s:443?type=grpc&security=tls&serviceName=%s&encryption=none&host=%s&fp=%s#%s",
		client.UUID, s.Domain, s.Transport.GRPCService(), s.Domain, s.Fingerprint, linkLabel(client.Name, "grpc", s.Tag),
	)
}

func GenerateClientLinks(client config.Client, s config.Settings) []string {
	server := config.ResolveProtocols(s.Protocols)
	protos := client.Protocols
	if !client.ProtocolsExplicit || len(protos) == 0 {
		// Non-explicit lists are creation snapshots: they merge in new server protocols. Explicit is an allowlist — off stays off.
		protos = append(slices.Clone(protos), server...)
	}

	links := make([]string, 0)
	for _, proto := range protos {
		if !slices.Contains(server, proto) {
			continue
		}
		var link string
		switch config.InboundProtocol(proto) {
		case config.ProtoVLESSReality:
			link = GenerateVLESSRealityLink(client, s)
		case config.ProtoHysteria2:
			link = GenerateHysteria2Link(client, s)
		case config.ProtoVLESSWS:
			link = GenerateVLESSWSLink(client, s)
		case config.ProtoXHTTPStealth:
			link = GenerateXHTTPStealthLink(client, s)
		case config.ProtoVLESSHTTPUpgrade:
			link = GenerateVLESSHTTPUpgradeLink(client, s)
		case config.ProtoVLESSGRPC:
			link = GenerateVLESSGRPCLink(client, s)
		}
		// One link per protocol: the merge above and old rows can repeat one.
		if !slices.Contains(links, link) {
			links = append(links, link)
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
			client.SubHash = RandomHex(16)
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
