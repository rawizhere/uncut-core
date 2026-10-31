package config

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/transport"
)

type AppConfig struct {
	Domain string
	Email  string
	// No default on purpose: unset TAG renders no tag token on labels.
	Tag       string
	Timezone  string
	Clients   string
	LogLevel  string
	Bandwidth string
}

// Version is a fallback for plain go build; images inject the tag with -ldflags -X.
var Version = "3.2.0"

// LoadAppConfig reads the process environment; compose's env_file supplies deployments/.env.
func LoadAppConfig() *AppConfig {
	return &AppConfig{
		Domain:    os.Getenv("DOMAIN"),
		Email:     os.Getenv("EMAIL"),
		Tag:       os.Getenv("TAG"),
		Timezone:  cmp.Or(os.Getenv("TZ"), "Europe/Moscow"),
		Clients:   os.Getenv("CLIENTS"),
		LogLevel:  cmp.Or(os.Getenv("LOG_LEVEL"), "info"),
		Bandwidth: os.Getenv("BANDWIDTH"),
	}
}

func InitLogger(level string) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(strings.TrimSpace(level))); err != nil {
		parsed = slog.LevelInfo
	}

	// Logs go to stderr; --json output on stdout must stay parseable.
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parsed})
	slog.SetDefault(slog.New(handler))
}

type InboundProtocol string

const (
	ProtoXHTTPStealth     InboundProtocol = "xhttp-stealth"
	ProtoVLESSWS          InboundProtocol = "vless-ws"
	ProtoVLESSHTTPUpgrade InboundProtocol = "vless-httpupgrade"
	ProtoVLESSGRPC        InboundProtocol = "vless-grpc"
	ProtoVLESSReality     InboundProtocol = "vless-reality"
	ProtoHysteria2        InboundProtocol = "hysteria2"
)

// Ports are the fixed loopback listeners behind the 443 split, named once for sing-box, nginx, the daemon and doctor.
var Ports = LoopbackPorts{
	WS: 10001, XHTTP: 10002, GRPC: 10003, Upgrade: 10004,
	Reality: 8443, HTTPSBackend: 8442,
	MTProtoBridge: 2398, MTProtoTLS: 2399,
	DoH: 3053,
}

type LoopbackPorts struct {
	WS, XHTTP, GRPC, Upgrade int
	// Reality is the reality inbound; HTTPSBackend the nginx https server, both fed by the SNI split.
	Reality, HTTPSBackend int
	// MTProtoBridge is the plain mtproto-proxy only tproxy reaches; MTProtoTLS the sing-box FakeTLS inbound.
	MTProtoBridge, MTProtoTLS int
	DoH                       int
}

// ProtocolPort is the loopback TCP inbound of a protocol; false for hysteria2, which owns UDP 443.
func ProtocolPort(p string) (int, bool) {
	port, ok := map[InboundProtocol]int{
		ProtoVLESSWS:          Ports.WS,
		ProtoXHTTPStealth:     Ports.XHTTP,
		ProtoVLESSGRPC:        Ports.GRPC,
		ProtoVLESSHTTPUpgrade: Ports.Upgrade,
		ProtoVLESSReality:     Ports.Reality,
	}[InboundProtocol(p)]
	return port, ok
}

// DefaultProtocols is the one protocol list: every known protocol, on by default, in render order.
var DefaultProtocols = []InboundProtocol{
	ProtoXHTTPStealth,
	ProtoVLESSWS,
	ProtoVLESSHTTPUpgrade,
	ProtoVLESSGRPC,
	ProtoVLESSReality,
	ProtoHysteria2,
}

func isValidProtocol(p string) bool {
	return slices.Contains(DefaultProtocols, InboundProtocol(p))
}

// DefaultProtocolNames is DefaultProtocols in the stored string form.
func DefaultProtocolNames() []string {
	out := make([]string, len(DefaultProtocols))
	for i, p := range DefaultProtocols {
		out[i] = string(p)
	}
	return out
}

// CleanProtocols trims, drops empties and duplicates, and rejects unknown names.
func CleanProtocols(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		if !isValidProtocol(p) {
			return nil, fmt.Errorf("unknown protocol %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

// ResolveProtocols is the server's active set: unknown names are skipped so a stored list never breaks rendering; empty means defaults.
func ResolveProtocols(raw []string) []string {
	known := slices.DeleteFunc(slices.Clone(raw), func(p string) bool { return !isValidProtocol(strings.TrimSpace(p)) })
	out, _ := CleanProtocols(known)
	if len(out) == 0 {
		return DefaultProtocolNames()
	}
	return out
}

// EffectiveProtocols is the one rule for what a client gets: an explicit list is an allowlist (off stays off), anything else is its creation snapshot plus the server set; both cut to the server's active set. Client order first, then the server's, so subscriptions stay byte-stable.
func EffectiveProtocols(c Client, serverRaw []string) []string {
	server := ResolveProtocols(serverRaw)
	protos := c.Protocols
	if !c.ProtocolsExplicit || len(protos) == 0 {
		protos = append(slices.Clone(protos), server...)
	}
	out := make([]string, 0, len(server))
	for _, p := range protos {
		if slices.Contains(server, p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

type Client struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Password string `json:"password"`
	SubHash  string `json:"sub_hash"`
	// ProtocolsExplicit true: the list is an allowlist; false: a snapshot that inherits the server's set.
	ProtocolsExplicit bool      `json:"protocols_explicit,omitempty"`
	Protocols         []string  `json:"protocols"`
	CreatedAt         time.Time `json:"created_at"`
}

type Settings struct {
	Domain            string `json:"domain,omitempty"`
	IP                string `json:"ip,omitempty"`
	Tag               string `json:"tag,omitempty"`
	Email             string `json:"email,omitempty"`
	RealityPrivKey    string `json:"reality_priv_key,omitempty"`
	RealityPubKey     string `json:"reality_pub_key,omitempty"`
	RealityShortID    string `json:"reality_short_id,omitempty"`
	RealityServerName string `json:"reality_server_name,omitempty"`
	// Hysteria2Obfs: salamander obfs password; empty renders no obfs block.
	Hysteria2Obfs string `json:"hysteria2_obfs,omitempty"`
	// Hysteria2HopPorts: client port-hopping range; set only when the host DNATs it onto 443.
	Hysteria2HopPorts string `json:"hysteria2_hop_ports,omitempty"`
	// Hysteria2UpMbps/DownMbps: server brutal-CC bandwidth caps; empty renders no fields and keeps BBR.
	Hysteria2UpMbps   string   `json:"hysteria2_up_mbps,omitempty"`
	Hysteria2DownMbps string   `json:"hysteria2_down_mbps,omitempty"`
	Protocols         []string `json:"protocols,omitempty"`
	InstallDir        string   `json:"install_dir,omitempty"`
	SubsDir           string   `json:"subs_dir,omitempty"`
	LogDir            string   `json:"log_dir,omitempty"`
	MTProtoRawSecret  string   `json:"mtproto_raw_secret,omitempty"`
	// WebProxySecret: the tproxy bridge's own 16 bytes, so a leaked webproxy link cannot open the FakeTLS branch.
	WebProxySecret string `json:"webproxy_secret,omitempty"`
	// Fingerprint is the uTLS fingerprint clients put in their ClientHello; a client-side parameter, the server never sees it.
	Fingerprint string `json:"fingerprint,omitempty"`
	// MTProtoTLSDomain is the optional third-party domain for the FakeTLS branch. Empty means the branch is off.
	MTProtoTLSDomain string               `json:"mtproto_tls_domain,omitempty"`
	Transport        *transport.Transport `json:"transport,omitempty"`
}

const (
	DefaultDomain = "node.example.com"
	DefaultSNI    = "dl.google.com"
	// Hysteria2 owns UDP 443 (the splitter is TCP-only); 8443 stays loopback.
	Hysteria2Port = "443"
	// Hysteria2HopPortsDefault: the DNAT range install.sh sets up on the host.
	Hysteria2HopPortsDefault  = "20000:30000"
	DefaultHysteria2Bandwidth = "500"
	DefaultTelegramProxyPort  = "8080"
	DefaultFingerprint        = "firefox"
)
