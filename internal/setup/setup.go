package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/renameio/v2"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/nginx"
	"github.com/rawizhere/uncut-core/internal/singbox"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/tproxy"
	"golang.org/x/crypto/curve25519"
)

const (
	saltLength = 12
	tokenChars = "abcdefghijklmnopqrstuvwxyz0123456789"
)

type Options struct {
	DataDir    string
	InstallDir string
	WebRoot    string
	SubsDir    string
	LogDir     string
	Staging    bool
}

func DefaultOptions(dataDir, installDir string) Options {
	return Options{
		DataDir:    dataDir,
		InstallDir: installDir,
		WebRoot:    "/var/www/html",
		SubsDir:    "/var/www/cdn/subs",
		LogDir:     filepath.Join(dataDir, "logs", "nginx"),
	}
}

func (o Options) CertsDir() string {
	return filepath.Join(o.InstallDir, "certs", "certificates")
}

func Ensure(ctx context.Context, store *db.Store, cfg *config.AppConfig, opts Options) (config.Settings, error) {
	if _, err := setting(store, "domain", cfg.Domain); err != nil {
		return config.Settings{}, fmt.Errorf("domain: %w", err)
	}

	if _, _, err := core.SyncServerIP(ctx, store, nil, false); err != nil {
		slog.Warn("Public IP detection failed", "error", err)
	}

	if err := ensureRealityKeys(store); err != nil {
		return config.Settings{}, err
	}

	if _, err := generate(store, "reality_short_id", func() (string, error) { return randomHex(8) }); err != nil {
		return config.Settings{}, err
	}

	sni, err := setting(store, "sni", config.DefaultSNI)
	if err != nil {
		return config.Settings{}, err
	}
	if _, err := setting(store, "reality_server_name", sni); err != nil {
		return config.Settings{}, err
	}

	generators := map[string]func() (string, error){
		"protocol_salt":  func() (string, error) { return randomToken(saltLength) },
		"cdn_pop":        func() (string, error) { return nginx.RandomPOP(), nil },
		"aws_request_id": func() (string, error) { return randomToken(54) },
		"host_id":        func() (string, error) { return randomToken(27) },
		"cdn_edge_id": func() (string, error) {
			suffix, err := randomHex(7)
			if err != nil {
				return "", err
			}
			return "d" + suffix, nil
		},
		"mtproto_raw_secret": func() (string, error) {
			return randomHex(16)
		},
		"mtproto_secret": func() (string, error) {
			raw, _ := store.GetSetting("mtproto_raw_secret")
			if raw == "" {
				var err error
				raw, err = randomHex(16)
				if err != nil {
					return "", err
				}
				_ = store.SetSetting("mtproto_raw_secret", raw)
			}
			sni, _ := store.GetSetting("sni")
			if sni == "" {
				sni = config.DefaultSNI
			}
			return fmt.Sprintf("ee%s%x", raw, sni), nil
		},
		"sub_salt": func() (string, error) {
			return randomToken(16)
		},
		"tuic_password": func() (string, error) {
			return randomToken(16)
		},
		"tuic_uuid": func() (string, error) {
			return randomHex(16)
		},
	}
	for key, fn := range generators {
		if _, err := generate(store, key, fn); err != nil {
			return config.Settings{}, err
		}
	}

	for key, fallback := range map[string]string{
		"tuic_port":           config.DefaultTUICPort,
		"telegram_proxy_port": config.DefaultTelegramProxyPort,
		"protocols":           strings.Join(stringProtocols(config.ValidProtocols), ","),
	} {
		if _, err := setting(store, key, fallback); err != nil {
			return config.Settings{}, err
		}
	}

	optional := map[string]string{
		"email":   cfg.Email,
		"country": cfg.Country,
	}
	for key, value := range optional {
		if value == "" {
			continue
		}
		if err := store.SetSetting(key, value); err != nil {
			return config.Settings{}, fmt.Errorf("save setting %q: %w", key, err)
		}
	}

	return Load(store, opts)
}

func Load(store *db.Store, opts Options) (config.Settings, error) {
	settings := config.Settings{
		InstallDir:        opts.InstallDir,
		SubsDir:           opts.SubsDir,
		LogDir:            opts.LogDir,
		Protocols:         splitList(get(store, "protocols")),
		Domain:            get(store, "domain"),
		IP:                get(store, "server_ip"),
		Country:           get(store, "country"),
		Email:             get(store, "email"),
		RealityPrivKey:    get(store, "reality_private_key"),
		RealityPubKey:     get(store, "reality_public_key"),
		RealityShortID:    get(store, "reality_short_id"),
		RealityServerName: get(store, "reality_server_name"),
		SNI:               get(store, "sni"),
		ProtocolSalt:      get(store, "protocol_salt"),
		TUICPort:          get(store, "tuic_port"),
		TUICPassword:      get(store, "tuic_password"),
		TUICUUID:          get(store, "tuic_uuid"),
		MTProtoSecret:     get(store, "mtproto_secret"),
		MTProtoRawSecret:  get(store, "mtproto_raw_secret"),
		TelegramProxyPort: get(store, "telegram_proxy_port"),
		CFEdgeID:          get(store, "cdn_edge_id"),
		CFPop:             get(store, "cdn_pop"),
		AWSReqID:          get(store, "aws_request_id"),
		HostID:            get(store, "host_id"),
		DPIFragment:       get(store, "dpi_fragment"),
		DPIPadding:        get(store, "dpi_padding"),
	}

	if settings.Domain == "" {
		return config.Settings{}, fmt.Errorf("domain is not configured")
	}
	return settings, nil
}

func RebuildAll(ctx context.Context, store *db.Store, sup *supervisor.Supervisor, opts Options) error {
	settings, err := Load(store, opts)
	if err != nil {
		return err
	}

	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("load clients: %w", err)
	}

	if err := core.RegenerateAllSubscriptions(store, opts.SubsDir, settings); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}

	sbConfig, err := singbox.GenerateConfig(settings, clients)
	if err != nil {
		return fmt.Errorf("generate sing-box config: %w", err)
	}
	if err := write(filepath.Join(opts.InstallDir, "config.json"), sbConfig, 0o600); err != nil {
		return err
	}

	genOpts, err := generatorOptions(settings, opts)
	if err != nil {
		return err
	}
	if err := nginx.WriteFiles(genOpts, nginx.DetectPaths(opts.InstallDir)); err != nil {
		return fmt.Errorf("write nginx files: %w", err)
	}

	if err := tproxy.WriteConfig(opts.InstallDir, settings.Domain, settings.MTProtoRawSecret); err != nil {
		return fmt.Errorf("write tproxy config: %w", err)
	}

	if sup != nil {
		if err := sup.ReloadSingBox(); err != nil {
			slog.Warn("Failed to gracefully reload sing-box, attempting process restart", "error", err)
			_ = sup.RestartProcess("sing-box")
		}
		if err := sup.ReloadNginx(); err != nil {
			slog.Warn("Failed to reload nginx", "error", err)
		}
	} else {
		_ = exec.Command("pkill", "-HUP", "sing-box").Run()
		_ = exec.Command("nginx", "-s", "reload").Run()
	}

	slog.Info("Configurations rebuilt and reloaded gracefully", "clients", len(clients), "domain", settings.Domain)
	return nil
}

func generatorOptions(settings config.Settings, opts Options) (nginx.GeneratorOptions, error) {
	port := 8080
	if settings.TelegramProxyPort != "" {
		parsed, err := strconv.Atoi(settings.TelegramProxyPort)
		if err != nil {
			return nginx.GeneratorOptions{}, fmt.Errorf("parse telegram proxy port: %w", err)
		}
		port = parsed
	}

	return nginx.GeneratorOptions{
		Domain:            settings.Domain,
		InstallDir:        opts.InstallDir,
		ProtocolSalt:      settings.ProtocolSalt,
		SubsDir:           settings.SubsDir,
		LogDir:            settings.LogDir,
		WebRoot:           opts.WebRoot,
		ActiveProtocols:   settings.Protocols,
		CFEdgeID:          settings.CFEdgeID,
		CFPop:             settings.CFPop,
		AWSReqID:          settings.AWSReqID,
		HostID:            settings.HostID,
		TelegramProxyPort: port,
	}, nil
}

func ensureRealityKeys(store *db.Store) error {
	if get(store, "reality_private_key") != "" && get(store, "reality_public_key") != "" {
		return nil
	}

	privKey := make([]byte, 32)
	if _, err := rand.Read(privKey); err != nil {
		return fmt.Errorf("generate reality private key: %w", err)
	}

	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	pubKey, err := curve25519.X25519(privKey, curve25519.Basepoint)
	if err != nil {
		return fmt.Errorf("calculate reality public key: %w", err)
	}

	privStr := base64.RawURLEncoding.EncodeToString(privKey)
	pubStr := base64.RawURLEncoding.EncodeToString(pubKey)

	if err := store.SetSetting("reality_private_key", privStr); err != nil {
		return fmt.Errorf("save reality private key: %w", err)
	}
	if err := store.SetSetting("reality_public_key", pubStr); err != nil {
		return fmt.Errorf("save reality public key: %w", err)
	}

	slog.Info("Generated Reality key pair")
	return nil
}

func setting(store *db.Store, key, fallback string) (string, error) {
	if value := get(store, key); value != "" {
		return value, nil
	}
	if fallback == "" {
		return "", fmt.Errorf("setting %q is not configured", key)
	}
	if err := store.SetSetting(key, fallback); err != nil {
		return "", fmt.Errorf("save setting %q: %w", key, err)
	}
	return fallback, nil
}

func generate(store *db.Store, key string, fn func() (string, error)) (string, error) {
	if value := get(store, key); value != "" {
		return value, nil
	}

	value, err := fn()
	if err != nil {
		return "", fmt.Errorf("generate %q: %w", key, err)
	}
	if err := store.SetSetting(key, value); err != nil {
		return "", fmt.Errorf("save setting %q: %w", key, err)
	}
	return value, nil
}

func get(store *db.Store, key string) string {
	value, err := store.GetSetting(key)
	if err != nil {
		return ""
	}
	return value
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(tokenChars))))
		if err != nil {
			return "", err
		}
		buf[i] = tokenChars[idx.Int64()]
	}
	return string(buf), nil
}

func write(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func stringProtocols(protos []config.InboundProtocol) []string {
	out := make([]string, 0, len(protos))
	for _, p := range protos {
		out = append(out, string(p))
	}
	return out
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
