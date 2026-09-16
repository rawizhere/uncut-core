package singbox

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rawizhere/uncut-core/internal/config"
)

type Config struct {
	Log       LogConfig        `json:"log"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []OutboundConfig `json:"outbounds"`
}

type LogConfig struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

type OutboundConfig struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

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

	cfg := Config{
		Log: LogConfig{
			Level:     "info",
			Timestamp: true,
		},
		Inbounds: inbounds,
		Outbounds: []OutboundConfig{
			{
				Type: "direct",
				Tag:  "direct",
			},
		},
	}

	return json.MarshalIndent(cfg, "", "  ")
}

func generateInbound(proto string, settings config.Settings, clients []config.Client) (map[string]any, error) {

	installDir := settings.InstallDir
	if installDir == "" {
		installDir = "/opt/sing-box"
	}

	switch proto {
	case string(config.ProtoVLESSWS):
		users := filterVLESSUsers(clients, proto)
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
		users := filterVLESSUsers(clients, proto)
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
		users := filterVLESSUsers(clients, proto)
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
		users := filterVLESSUsers(clients, proto)
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
		users := filterRealityUsers(clients, proto)
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

	case string(config.ProtoTUIC):
		users := filterTUICUsers(clients, proto)
		port := 8443 // matches config.DefaultTUICPort
		if settings.TUICPort != "" {
			if p, err := strconv.Atoi(settings.TUICPort); err == nil {
				port = p
			}
		}

		domain := settings.Domain
		certPath := filepath.Join(installDir, "certs", "certificates", domain+".crt")
		keyPath := filepath.Join(installDir, "certs", "certificates", domain+".key")

		return map[string]any{
			"type":               "tuic",
			"tag":                "tuic",
			"listen":             "0.0.0.0",
			"listen_port":        port,
			"users":              users,
			"congestion_control": "bbr",
			"auth_timeout":       "3s",
			"zero_rtt_handshake": true,
			"heartbeat":          "10s",
			"tls": map[string]any{
				"enabled":          true,
				"alpn":             []string{"h3"},
				"certificate_path": certPath,
				"key_path":         keyPath,
			},
		}, nil
	}

	return nil, nil
}

func clientHasProto(client config.Client, proto string) bool {
	if len(client.Protocols) == 0 {
		return true
	}
	for _, p := range client.Protocols {
		p = strings.TrimSpace(p)
		if p == proto {
			return true
		}
	}
	return false
}

func filterVLESSUsers(clients []config.Client, proto string) []map[string]string {
	users := make([]map[string]string, 0)
	for _, c := range clients {
		if c.UUID != "" && clientHasProto(c, proto) {
			users = append(users, map[string]string{"uuid": c.UUID})
		}
	}
	return users
}

func filterRealityUsers(clients []config.Client, proto string) []map[string]string {
	users := make([]map[string]string, 0)
	for _, c := range clients {
		if c.UUID != "" && clientHasProto(c, proto) {
			users = append(users, map[string]string{
				"uuid": c.UUID,
				"flow": "xtls-rprx-vision",
			})
		}
	}
	return users
}

func filterTUICUsers(clients []config.Client, proto string) []map[string]string {
	users := make([]map[string]string, 0)
	for _, c := range clients {
		if c.UUID != "" && clientHasProto(c, proto) {
			pass := c.Password
			if pass == "" {
				pass = c.UUID
			}
			users = append(users, map[string]string{
				"uuid":     c.UUID,
				"password": pass,
				"name":     c.Name,
			})
		}
	}
	return users
}
