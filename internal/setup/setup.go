package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	"github.com/rawizhere/uncut-core/internal/transport"
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
	if cfg.Domain != "" {
		currentDomain, _ := store.GetSetting("domain")
		if currentDomain != cfg.Domain {
			if err := store.SetSetting("domain", cfg.Domain); err != nil {
				return config.Settings{}, fmt.Errorf("domain: %w", err)
			}
		}
	} else if _, err := setting(store, "domain", config.DefaultDomain); err != nil {
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

	generators := map[string]func() (string, error){
		"mtproto_raw_secret": func() (string, error) {
			return randomHex(16)
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
		"protocols":           strings.Join(stringProtocols(config.DefaultProtocols), ","),
	} {
		if _, err := setting(store, key, fallback); err != nil {
			return config.Settings{}, err
		}
	}

	// 8443 was the old default (closed firewall port), not a choice: migrate it; custom values stay.
	if get(store, "tuic_port") == "8443" {
		if err := store.SetSetting("tuic_port", config.DefaultTUICPort); err != nil {
			return config.Settings{}, fmt.Errorf("tuic port upgrade: %w", err)
		}
	}

	// Stored protocol lists are first-init snapshots, not choices: merge in
	// new defaults or subscriptions omit them. Explicit lists stay alone.
	if err := mergeDefaultProtocols(store); err != nil {
		return config.Settings{}, err
	}

	optional := map[string]string{
		"email": cfg.Email,
		"tag":   cfg.Tag,
	}
	for key, value := range optional {
		if value == "" {
			continue
		}
		if err := store.SetSetting(key, value); err != nil {
			return config.Settings{}, fmt.Errorf("save setting %q: %w", key, err)
		}
	}

	clients, err := store.GetClients()
	if err == nil && len(clients) == 0 {
		clientNames := cfg.Clients
		if clientNames == "" {
			clientNames = "admin"
		}
		for _, name := range strings.Split(clientNames, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, err := core.AddClient(store, name, nil); err != nil {
				slog.Warn("Failed to seed initial client", "name", name, "error", err)
			}
		}
	}

	return Load(store, opts)
}

func Load(store *db.Store, opts Options) (config.Settings, error) {
	var err error
	settings := config.Settings{
		InstallDir:        opts.InstallDir,
		SubsDir:           opts.SubsDir,
		LogDir:            opts.LogDir,
		Protocols:         splitList(get(store, "protocols")),
		Domain:            get(store, "domain"),
		IP:                get(store, "server_ip"),
		Tag:               get(store, "tag"),
		Email:             get(store, "email"),
		RealityPrivKey:    get(store, "reality_private_key"),
		RealityPubKey:     get(store, "reality_public_key"),
		RealityShortID:    get(store, "reality_short_id"),
		TUICPort:          get(store, "tuic_port"),
		TUICPassword:      get(store, "tuic_password"),
		TUICUUID:          get(store, "tuic_uuid"),
		MTProtoRawSecret:  get(store, "mtproto_raw_secret"),
		MTProtoTLSDomain:  get(store, "mtproto_tls_domain"),
		TelegramProxyPort: get(store, "telegram_proxy_port"),
	}

	settings.Transport, err = loadTransport(store)
	if err != nil {
		return config.Settings{}, err
	}
	// The transport is the sole source of the reality SNI; it travels with the rev.
	settings.RealityServerName = settings.Transport.RealitySNI()
	if settings.Domain == "" {
		return config.Settings{}, fmt.Errorf("domain is not configured")
	}
	return settings, nil
}

// loadTransport: stored transport, migrated legend, or freshly generated.
func loadTransport(store *db.Store) (*transport.Transport, error) {
	raw := get(store, "transport")
	if raw != "" {
		var t transport.Transport
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, fmt.Errorf("parse transport: %w", err)
		}
		return &t, nil
	}
	if legacyRaw := get(store, "legend"); legacyRaw != "" {
		var l transport.LegacyTemplates
		if err := json.Unmarshal([]byte(legacyRaw), &l); err != nil {
			return nil, fmt.Errorf("parse legacy legend: %w", err)
		}
		t := transport.FromLegacy(&l, get(store, "protocol_salt"))
		if err := SaveTransport(store, t); err != nil {
			return nil, err
		}
		for _, key := range []string{"legend", "protocol_salt"} {
			if err := store.SetSetting(key, ""); err != nil {
				return nil, fmt.Errorf("clear legacy %q: %w", key, err)
			}
		}
		return t, nil
	}
	t, err := transport.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate transport: %w", err)
	}
	if err := SaveTransport(store, t); err != nil {
		return nil, err
	}
	return t, nil
}

// SaveTransport stores the node's transport. Nil is refused: a missing transport would render obvious paths.
func SaveTransport(store *db.Store, t *transport.Transport) error {
	if t == nil {
		return fmt.Errorf("transport: nil")
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode transport: %w", err)
	}
	return store.SetSetting("transport", string(data))
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

	if err := tproxy.WriteConfig(opts.InstallDir, settings.Domain, settings.MTProtoRawSecret, tproxy.DefaultPublicDir); err != nil {
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
		SubsDir:           settings.SubsDir,
		LogDir:            settings.LogDir,
		WebRoot:           opts.WebRoot,
		ActiveProtocols:   settings.Protocols,
		TelegramProxyPort: port,
		Transport:         settings.Transport,
		RealityServerName: settings.RealityServerName,
		MTProtoTLSDomain:  settings.MTProtoTLSDomain,
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

// mergeDefaultProtocols appends post-init default protocols to non-explicit
// lists; explicit lists stay alone (merging resurrected disabled protocols).
func mergeDefaultProtocols(store *db.Store) error {
	defaults := stringProtocols(config.DefaultProtocols)

	if get(store, "protocols_explicit") != "true" {
		stored := splitList(get(store, "protocols"))
		merged, added := mergeMissing(stored, defaults)
		if added > 0 {
			if err := store.SetSetting("protocols", strings.Join(merged, ",")); err != nil {
				return fmt.Errorf("merge protocols: %w", err)
			}
			slog.Info("Added protocols introduced by the upgrade", "added", added)
		}
	}

	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("fetch clients: %w", err)
	}
	for _, client := range clients {
		if client.ProtocolsExplicit {
			// An explicit allowlist is a choice: off stays off, like the server marker.
			continue
		}
		clientMerged, clientAdded := mergeMissing(client.Protocols, defaults)
		if clientAdded == 0 {
			continue
		}
		client.Protocols = clientMerged
		if err := store.AddClient(client); err != nil {
			return fmt.Errorf("merge protocols for client %s: %w", client.Name, err)
		}
		slog.Info("Added protocols to client", "name", client.Name, "added", clientAdded)
	}
	return nil
}

// mergeMissing appends the missing entries in want order; returns the count.
func mergeMissing(base, want []string) ([]string, int) {
	seen := make(map[string]bool, len(base))
	for _, p := range base {
		seen[strings.TrimSpace(p)] = true
	}
	out := append([]string(nil), base...)
	added := 0
	for _, p := range want {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		added++
	}
	return out, added
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
