package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type AppConfig struct {
	Domain         string `env:"DOMAIN"`
	Email          string `env:"EMAIL"`
	Country        string `env:"COUNTRY" envDefault:"US"`
	Timezone       string `env:"TZ" envDefault:"Europe/Moscow"`
	DataDir        string `env:"DATA_DIR" envDefault:"/opt/uncut/data"`
	InstallDir     string `env:"INSTALL_DIR" envDefault:"/opt/sing-box"`
	SubsDir        string `env:"SUBS_DIR" envDefault:"/var/www/cdn/subs"`
	LogDir         string `env:"LOG_DIR"`
	WebRoot        string `env:"WEB_ROOT" envDefault:"/var/www/html"`
	ACMEStaging    bool   `env:"ACME_STAGING" envDefault:"false"`
	ZeroSSLEABKID  string `env:"ZEROSSL_EAB_KID"`
	ZeroSSLEABHMAC string `env:"ZEROSSL_EAB_HMAC"`
	Clients        string `env:"CLIENTS"`
	LogLevel       string `env:"LOG_LEVEL" envDefault:"info"`
}

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

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed})
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

type Client struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	Password  string    `json:"password"`
	SubHash   string    `json:"sub_hash"`
	Protocols []string  `json:"protocols"`
	CreatedAt time.Time `json:"created_at"`
}

type Settings struct {
	Domain            string   `json:"domain,omitempty"`
	IP                string   `json:"ip,omitempty"`
	Country           string   `json:"country,omitempty"`
	Email             string   `json:"email,omitempty"`
	RealityPrivKey    string   `json:"reality_priv_key,omitempty"`
	RealityPubKey     string   `json:"reality_pub_key,omitempty"`
	RealityShortID    string   `json:"reality_short_id,omitempty"`
	RealityServerName string   `json:"reality_server_name,omitempty"`
	TUICPort          string   `json:"tuic_port,omitempty"`
	TUICPassword      string   `json:"tuic_password,omitempty"`
	TUICUUID          string   `json:"tuic_uuid,omitempty"`
	Protocols         []string `json:"protocols,omitempty"`
	SNI               string   `json:"sni,omitempty"`
	ProtocolSalt      string   `json:"protocol_salt,omitempty"`
	SubSalt           string   `json:"sub_salt,omitempty"`
	InstallDir        string   `json:"install_dir,omitempty"`
	SubsDir           string   `json:"subs_dir,omitempty"`
	LogDir            string   `json:"log_dir,omitempty"`
	MTProtoSecret     string   `json:"mtproto_secret,omitempty"`
	MTProtoRawSecret  string   `json:"mtproto_raw_secret,omitempty"`
	TelegramProxyPort string   `json:"telegram_proxy_port,omitempty"`
	CFEdgeID          string   `json:"cf_edge_id,omitempty"`
	CFPop             string   `json:"cf_pop,omitempty"`
	AWSReqID          string   `json:"aws_req_id,omitempty"`
	HostID            string   `json:"host_id,omitempty"`
	DPIFragment       string   `json:"dpi_fragment,omitempty"`
	DPIPadding        string   `json:"dpi_padding,omitempty"`
}

const (
	DefaultSNI               = "dl.google.com"
	DefaultTUICPort          = "443"
	DefaultTelegramProxyPort = "8080"
	DefaultSubsDir           = "/var/www/cdn/subs"
	DefaultWebRoot           = "/var/www/html"
)

var mskLocation = time.FixedZone("MSK", 3*60*60)

func GetMSKTime() time.Time {
	return time.Now().In(mskLocation)
}
