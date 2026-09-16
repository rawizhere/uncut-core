package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
	"github.com/rawizhere/uncut-core/internal/transport"
)

type AppConfig struct {
	Domain string `env:"DOMAIN"`
	Email  string `env:"EMAIL"`
	// No envDefault on purpose: unset TAG renders no tag token on labels.
	Tag      string `env:"TAG"`
	Timezone string `env:"TZ" envDefault:"Europe/Moscow"`
	Clients  string `env:"CLIENTS"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

// Version is a fallback for plain go build; images inject the tag with -ldflags -X.
var Version = "3.2.0"

func LoadAppConfig() (*AppConfig, error) {
	_ = godotenv.Load()
	_ = godotenv.Load("deployments/.env")

	cfg := &AppConfig{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	return cfg, nil
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
	ProtoTUIC             InboundProtocol = "tuic"
)

var DefaultProtocols = []InboundProtocol{
	ProtoXHTTPStealth,
	ProtoVLESSWS,
	ProtoVLESSHTTPUpgrade,
	ProtoVLESSGRPC,
	ProtoVLESSReality,
	ProtoTUIC,
}

var ValidProtocols = []InboundProtocol{
	ProtoXHTTPStealth,
	ProtoVLESSWS,
	ProtoVLESSHTTPUpgrade,
	ProtoVLESSGRPC,
	ProtoVLESSReality,
	ProtoTUIC,
}

func IsValidProtocol(p string) bool {
	for _, valid := range ValidProtocols {
		if string(valid) == p {
			return true
		}
	}
	return false
}

func ResolveProtocols(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || !IsValidProtocol(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) > 0 {
		return out
	}
	for _, p := range DefaultProtocols {
		out = append(out, string(p))
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
	Domain            string   `json:"domain,omitempty"`
	IP                string   `json:"ip,omitempty"`
	Tag               string   `json:"tag,omitempty"`
	Email             string   `json:"email,omitempty"`
	RealityPrivKey    string   `json:"reality_priv_key,omitempty"`
	RealityPubKey     string   `json:"reality_pub_key,omitempty"`
	RealityShortID    string   `json:"reality_short_id,omitempty"`
	RealityServerName string   `json:"reality_server_name,omitempty"`
	TUICPort          string   `json:"tuic_port,omitempty"`
	TUICPassword      string   `json:"tuic_password,omitempty"`
	TUICUUID          string   `json:"tuic_uuid,omitempty"`
	Protocols         []string `json:"protocols,omitempty"`
	SubSalt           string   `json:"sub_salt,omitempty"`
	InstallDir        string   `json:"install_dir,omitempty"`
	SubsDir           string   `json:"subs_dir,omitempty"`
	LogDir            string   `json:"log_dir,omitempty"`
	MTProtoRawSecret  string   `json:"mtproto_raw_secret,omitempty"`
	// MTProtoTLSDomain is the optional third-party domain for the FakeTLS branch. Empty means the branch is off.
	MTProtoTLSDomain  string               `json:"mtproto_tls_domain,omitempty"`
	TelegramProxyPort string               `json:"telegram_proxy_port,omitempty"`
	Transport         *transport.Transport `json:"transport,omitempty"`
}

const (
	DefaultDomain = "node.example.com"
	DefaultSNI    = "dl.google.com"
	// TUIC owns UDP 443 (the splitter is TCP-only); 8443 stays loopback.
	// Changing this invalidates existing TUIC links: re-issue.
	DefaultTUICPort          = "443"
	DefaultTelegramProxyPort = "8080"
	DefaultSubsDir           = "/var/www/cdn/subs"
	DefaultWebRoot           = "/var/www/html"
)

// TelegramProxyPort: nginx and tproxy must read the same number or the bridge serves an empty page.
func TelegramProxyPort() int {
	port, err := strconv.Atoi(DefaultTelegramProxyPort)
	if err != nil || port <= 0 {
		return 8080
	}
	return port
}

var mskLocation = time.FixedZone("MSK", 3*60*60)

func GetMSKTime() time.Time {
	return time.Now().In(mskLocation)
}
