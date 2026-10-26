package singbox

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/links"
)

func GenerateConfig(settings config.Settings, clients []config.Client) ([]byte, error) {
	inbounds := make([]map[string]any, 0)

	for _, proto := range config.ResolveProtocols(settings.Protocols) {
		inbound, err := generateInbound(proto, settings, clients)
		if err != nil {
			return nil, fmt.Errorf("generate inbound for %s: %w", proto, err)
		}
		if inbound != nil {
			inbounds = append(inbounds, inbound)
		}
	}
	if settings.MTProtoTLSDomain != "" && settings.MTProtoRawSecret != "" {
		inbounds = append(inbounds, mtproxyInbound(settings))
	}

	return json.MarshalIndent(map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []map[string]string{{"type": "direct", "tag": "direct"}},
	}, "", "  ")
}

// allowedUsers renders every client allowed on proto; an empty client list means all protocols.
func allowedUsers(clients []config.Client, proto string, user func(config.Client) map[string]string) []map[string]string {
	out := make([]map[string]string, 0)
	for _, c := range clients {
		if c.UUID != "" && (len(c.Protocols) == 0 || slices.Contains(c.Protocols, proto)) {
			out = append(out, user(c))
		}
	}
	return out
}

func vlessUser(c config.Client) map[string]string { return map[string]string{"uuid": c.UUID} }

// mtproxyInbound is the FakeTLS branch behind the SNI map; mtg parses the same ee-secret the client link carries, so issued links stay valid.
func mtproxyInbound(settings config.Settings) map[string]any {
	return map[string]any{
		"type":        "mtproxy",
		"tag":         "mtproxy-faketls",
		"listen":      "127.0.0.1",
		"listen_port": 2399,
		"users": []map[string]string{{
			"name":   "default",
			"secret": links.MTProtoFakeTLSSecret(settings.MTProtoRawSecret, settings.MTProtoTLSDomain),
		}},
		// Probes without the secret are relayed to the mask domain's real 443.
		"domain_fronting_port": 443,
	}
}

func generateInbound(proto string, settings config.Settings, clients []config.Client) (map[string]any, error) {

	installDir := settings.InstallDir
	if installDir == "" {
		installDir = "/opt/sing-box"
	}

	switch proto {
	case string(config.ProtoVLESSWS):
		users := allowedUsers(clients, proto, vlessUser)
		return map[string]any{
			"type":        "vless",
			"tag":         "vless-ws",
			"listen":      "127.0.0.1",
			"listen_port": 10001,
			"users":       users,
			"transport": map[string]any{
				"type":                   "ws",
				"path":                   settings.Transport.WSPath(),
				"max_early_data":         2048,
				"early_data_header_name": "Sec-WebSocket-Protocol",
			},
		}, nil

	case string(config.ProtoXHTTPStealth):
		users := allowedUsers(clients, proto, vlessUser)
		return map[string]any{
			"type":        "vless",
			"tag":         "xhttp-stealth",
			"listen":      "127.0.0.1",
			"listen_port": 10002,
			"users":       users,
			"transport": map[string]any{
				"type": "xhttp",
				"path": settings.Transport.XHTTPPath(),
				// packet-up survives the nginx hop; stream-up stalls behind an h1 proxy.
				"mode":                   "packet-up",
				"x_padding_bytes":        "100-2500",
				"no_sse_header":          false,
				"sc_max_each_post_bytes": 1000000,
				"sc_max_buffered_posts":  30,
			},
		}, nil

	case string(config.ProtoVLESSHTTPUpgrade):
		users := allowedUsers(clients, proto, vlessUser)
		return map[string]any{
			"type":        "vless",
			"tag":         "vless-httpupgrade",
			"listen":      "127.0.0.1",
			"listen_port": 10004,
			"users":       users,
			"transport": map[string]any{
				"type": "httpupgrade",
				"path": settings.Transport.UpgradePath(),
				"host": settings.Domain,
			},
		}, nil

	case string(config.ProtoVLESSGRPC):
		users := allowedUsers(clients, proto, vlessUser)
		return map[string]any{
			"type":        "vless",
			"tag":         "vless-grpc",
			"listen":      "127.0.0.1",
			"listen_port": 10003,
			"users":       users,
			"transport": map[string]any{
				"type":         "grpc",
				"service_name": settings.Transport.GRPCService(),
			},
		}, nil

	case string(config.ProtoVLESSReality):
		users := allowedUsers(clients, proto, func(c config.Client) map[string]string {
			return map[string]string{"uuid": c.UUID, "flow": "xtls-rprx-vision"}
		})
		// Client SNI = handshake target: nginx hands over the hello (ssl_preread).
		sni := settings.RealityServerName

		shortIDs := []string{}
		if settings.RealityShortID != "" {
			shortIDs = append(shortIDs, settings.RealityShortID)
		}

		return map[string]any{
			"type": "vless",
			"tag":  "vless-reality",
			// Loopback only: the stream splitter in nginx is the only client.
			"listen":      "127.0.0.1",
			"listen_port": 8443,
			"users":       users,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": sni,
				"reality": map[string]any{
					"enabled": true,
					"handshake": map[string]any{
						"server":      sni,
						"server_port": 443,
					},
					"private_key":         settings.RealityPrivKey,
					"short_id":            shortIDs,
					"max_time_difference": "5m",
				},
			},
		}, nil

	case string(config.ProtoHysteria2):
		users := allowedUsers(clients, proto, func(c config.Client) map[string]string {
			return map[string]string{"password": cmp.Or(c.Password, c.UUID)}
		})
		domain := settings.Domain
		certPath := filepath.Join(installDir, "certs", "certificates", domain+".crt")
		keyPath := filepath.Join(installDir, "certs", "certificates", domain+".key")
		// sing-box refuses to start on a missing cert file and takes reality down with it; the inbound appears with the first certificate, as nginx's ssl server does.
		for _, p := range []string{certPath, keyPath} {
			if _, err := os.Stat(p); err != nil {
				return nil, nil
			}
		}

		// sing-box requires a tls block even though the transport is QUIC; the LE cert keeps client verification honest.
		inbound := map[string]any{
			"type":        "hysteria2",
			"tag":         "hysteria2",
			"listen":      "0.0.0.0",
			"listen_port": 443,
			"users":       users,
			"tls": map[string]any{
				"enabled":          true,
				"certificate_path": certPath,
				"key_path":         keyPath,
			},
		}
		// Brutal runs only for clients that send their own rate, capped at the server values; everyone else gets BBR, and aggressive keeps more in flight on high-RTT links.
		inbound["bbr_profile"] = "aggressive"
		for key, raw := range map[string]string{"up_mbps": settings.Hysteria2UpMbps, "down_mbps": settings.Hysteria2DownMbps} {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err == nil && n > 0 {
				inbound[key] = n
			}
		}
		if settings.Hysteria2Obfs != "" {
			inbound["obfs"] = map[string]any{"type": "salamander", "password": settings.Hysteria2Obfs}
		}
		return inbound, nil
	}

	return nil, nil
}
