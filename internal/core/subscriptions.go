package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

// GenerateAmneziaWGLink renders the mihomo wireguard share link with the Amnezia 3.1 obfuscation params; a client without its private key (a peer seeded from outside) gets no link, since a half-known key builds a dead tunnel.
func GenerateAmneziaWGLink(client config.Client, s config.Settings) string {
	a := client.AWG
	if a == nil || a.PrivKey == "" {
		return ""
	}
	q := url.Values{}
	q.Set("address", a.IP)
	q.Set("publickey", s.AmneziaWGPubKey)
	if a.PSK != "" {
		q.Set("presharedkey", a.PSK)
	}
	q.Set("mtu", strconv.Itoa(config.AmneziaWGMTU))
	q.Set("header_protection_key", s.AmneziaWGHeaderKey)
	q.Set("content_padding_addition", config.AmneziaWGPadding)
	for k, v := range map[string]int{
		"jc": config.AmneziaWGJc, "jmin": config.AmneziaWGJmin, "jmax": config.AmneziaWGJmax,
		"s1": config.AmneziaWGS1, "s2": config.AmneziaWGS2, "s3": config.AmneziaWGS3, "s4": config.AmneziaWGS4,
		"h1": config.AmneziaWGH1, "h2": config.AmneziaWGH2, "h3": config.AmneziaWGH3, "h4": config.AmneziaWGH4,
	} {
		q.Set(k, strconv.Itoa(v))
	}
	return fmt.Sprintf("wireguard://%s@%s:%d?%s#%s",
		url.QueryEscape(a.PrivKey), s.Domain, s.AmneziaWGPort(), q.Encode(), linkLabel(client.Name, "amneziawg", s.Tag))
}

func GenerateClientLinks(client config.Client, s config.Settings) []string {
	links := make([]string, 0)
	for _, proto := range config.EffectiveProtocols(client, s.Protocols) {
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
		case config.ProtoAmneziaWG:
			link = GenerateAmneziaWGLink(client, s)
		}
		if link != "" {
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

// RegenerateAllSubscriptions writes one file per client and removes every other file in subDir.
func RegenerateAllSubscriptions(store *db.Store, subDir string, s config.Settings) error {
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return fmt.Errorf("create subscription dir: %w", err)
	}

	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("fetch clients: %w", err)
	}

	keep := make(map[string]bool, len(clients))
	for _, client := range clients {
		// Atomic: nginx may serve the file mid-write.
		if err := config.WriteFileAtomic(filepath.Join(subDir, client.SubHash), []byte(GenerateSubscriptionPayload(client, s)), 0o644); err != nil {
			return fmt.Errorf("write subscription for %s: %w", client.Name, err)
		}
		keep[client.SubHash] = true
	}
	// Sweep files of deleted clients and rotated tokens: their URLs must die with the change.
	entries, err := os.ReadDir(subDir)
	if err != nil {
		return fmt.Errorf("read subscription dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && !keep[e.Name()] {
			_ = os.Remove(filepath.Join(subDir, e.Name()))
		}
	}

	slog.Info("Regenerated all subscription files", "count", len(clients), "dir", subDir)
	return nil
}
