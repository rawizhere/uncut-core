package setup

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/nginx"
	"github.com/rawizhere/uncut-core/internal/singbox"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/tproxy"
	"github.com/rawizhere/uncut-core/internal/transport"
	"github.com/rawizhere/uncut-core/internal/updater"
	"golang.org/x/crypto/curve25519"
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

// Bootstrap prepares the node state once per daemon start: env seeds, keys, secrets, the public IP, protocol upgrades and the first clients. Every other command only Loads.
func Bootstrap(ctx context.Context, store *db.Store, cfg *config.AppConfig, opts Options) (config.Settings, error) {
	// DOMAIN seeds a fresh node only: after that the domain belongs to raw change-domain, and the env would silently revert it.
	if _, err := setting(store, "domain", cmp.Or(cfg.Domain, config.DefaultDomain)); err != nil {
		return config.Settings{}, fmt.Errorf("domain: %w", err)
	}

	if _, _, err := core.SyncServerIP(ctx, store, nil, false); err != nil {
		slog.Warn("Public IP detection failed", "error", err)
	}

	fresh, err := ensureRealityKeys(store)
	if err != nil {
		return config.Settings{}, err
	}

	// Node secrets: hex of n random bytes, generated once.
	for key, n := range map[string]int{"reality_short_id": 8, "mtproto_raw_secret": 16, "webproxy_secret": 16, "hysteria2_obfs": 16} {
		if get(store, key) != "" {
			continue
		}
		if err := store.SetSetting(key, core.RandomHex(n)); err != nil {
			return config.Settings{}, fmt.Errorf("save setting %q: %w", key, err)
		}
	}

	for key, fallback := range map[string]string{
		"hysteria2_hop_ports": config.Hysteria2HopPortsDefault,
		"protocols":           strings.Join(config.DefaultProtocolNames(), ","),
		"fingerprint":         config.DefaultFingerprint,
	} {
		if _, err := setting(store, key, fallback); err != nil {
			return config.Settings{}, err
		}
	}

	// Stored protocol lists are first-init snapshots, not choices: merge in new defaults or subscriptions omit them. Explicit lists stay alone.
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

	// Seed clients on the first init only: a node whose operator deleted every client must not grow admin back on restart.
	if fresh {
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
		Hysteria2Obfs:     get(store, "hysteria2_obfs"),
		Hysteria2HopPorts: get(store, "hysteria2_hop_ports"),
		Hysteria2UpMbps:   get(store, "hysteria2_up_mbps"),
		Hysteria2DownMbps: get(store, "hysteria2_down_mbps"),
		Fingerprint:       cmp.Or(get(store, "fingerprint"), config.DefaultFingerprint),
		MTProtoRawSecret:  get(store, "mtproto_raw_secret"),
		WebProxySecret:    get(store, "webproxy_secret"),
		MTProtoTLSDomain:  get(store, "mtproto_tls_domain"),
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

// loadTransport: the stored transport, or a freshly generated one.
func loadTransport(store *db.Store) (*transport.Transport, error) {
	if raw := get(store, "transport"); raw != "" {
		var t transport.Transport
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, fmt.Errorf("parse transport: %w", err)
		}
		return &t, nil
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

// RebuildAll renders every config, checks it with its own consumer before it lands, and reloads the running children; a nil sup renders only (the daemon's first pass, before anything runs). Only the daemon calls it — commands go through Reload.
func RebuildAll(ctx context.Context, store *db.Store, sup *supervisor.Supervisor, opts Options) error {
	settings, err := Load(store, opts)
	if err != nil {
		return err
	}

	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("load clients: %w", err)
	}

	sbConfig, err := singbox.GenerateConfig(settings, clients)
	if err != nil {
		return fmt.Errorf("generate sing-box config: %w", err)
	}
	sbPath := filepath.Join(opts.InstallDir, "config.json")
	if _, err := writeChecked(map[string][]byte{sbPath: sbConfig}, 0o600, func() error {
		return run(ctx, updater.SingBoxBin(opts.InstallDir), "check", "-c", sbPath)
	}); err != nil {
		return fmt.Errorf("sing-box config: %w", err)
	}

	ngFiles, err := nginx.Files(generatorOptions(settings, opts))
	if err != nil {
		return fmt.Errorf("render nginx files: %w", err)
	}
	if _, err := writeChecked(ngFiles, 0o644, func() error { return run(ctx, "nginx", "-t") }); err != nil {
		return fmt.Errorf("nginx config: %w", err)
	}

	tpFiles, err := tproxy.Files(opts.InstallDir, settings.Domain, settings.WebProxySecret, tproxy.DefaultPublicDir)
	if err != nil {
		return fmt.Errorf("render tproxy config: %w", err)
	}
	tpChanged, err := writeChecked(tpFiles, 0o600, nil)
	if err != nil {
		return fmt.Errorf("tproxy config: %w", err)
	}

	// Subscriptions last: they must never advertise a config that failed its check.
	if err := core.RegenerateAllSubscriptions(store, opts.SubsDir, settings); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}

	if sup != nil {
		if err := sup.ReloadSingBox(); err != nil {
			slog.Warn("Failed to gracefully reload sing-box, attempting process restart", "error", err)
			_ = sup.RestartProcess("sing-box")
		}
		if err := sup.ReloadNginx(); err != nil {
			slog.Warn("Failed to reload nginx", "error", err)
		}
		if tpChanged {
			if err := sup.RestartProcess("tproxy-server"); err != nil {
				slog.Warn("Failed to restart tproxy-server after its config changed", "error", err)
			}
		}
	}

	slog.Info("Configurations rebuilt", "clients", len(clients), "domain", settings.Domain, "reloaded", sup != nil)
	return nil
}

// writeChecked writes the files, runs check, and on failure puts the previous versions back so a bad render never replaces a working config; a file with no previous version stays, there is nothing better to serve. It reports whether any file changed.
func writeChecked(files map[string][]byte, perm os.FileMode, check func() error) (bool, error) {
	old := make(map[string][]byte, len(files))
	changed := false
	for path, data := range files {
		prev, err := os.ReadFile(path)
		if err == nil {
			old[path] = prev
		}
		if err == nil && bytes.Equal(prev, data) {
			continue
		}
		changed = true
		if err := config.WriteFileAtomic(path, data, perm); err != nil {
			return changed, err
		}
	}
	if !changed || check == nil {
		return changed, nil
	}
	if err := check(); err != nil {
		for path, prev := range old {
			_ = config.WriteFileAtomic(path, prev, perm)
		}
		return false, err
	}
	return true, nil
}

// run executes a config check and folds its output into the error, which is where the check says what is wrong.
func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DaemonAddr is the daemon's loopback control and probe listener.
const DaemonAddr = "127.0.0.1:8088"

// Reload asks the daemon to re-render and reload, restarting the named children after: the daemon is the only writer of configs and the only holder of the children.
func Reload(ctx context.Context, restart ...string) error {
	u := "http://" + DaemonAddr + "/reload"
	if len(restart) > 0 {
		u += "?restart=" + url.QueryEscape(strings.Join(restart, ","))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("daemon not reachable, the change is saved and applies on its next start: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("daemon reload: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func generatorOptions(settings config.Settings, opts Options) nginx.GeneratorOptions {
	return nginx.GeneratorOptions{
		Domain:            settings.Domain,
		InstallDir:        opts.InstallDir,
		SubsDir:           settings.SubsDir,
		LogDir:            settings.LogDir,
		WebRoot:           opts.WebRoot,
		ActiveProtocols:   settings.Protocols,
		Transport:         settings.Transport,
		RealityServerName: settings.RealityServerName,
		MTProtoTLSDomain:  settings.MTProtoTLSDomain,
	}
}

// ensureRealityKeys generates the reality pair once; true means it just did, which marks the node's first init.
func ensureRealityKeys(store *db.Store) (bool, error) {
	if get(store, "reality_private_key") != "" && get(store, "reality_public_key") != "" {
		return false, nil
	}

	privKey := make([]byte, 32)
	if _, err := rand.Read(privKey); err != nil {
		return false, fmt.Errorf("generate reality private key: %w", err)
	}

	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	pubKey, err := curve25519.X25519(privKey, curve25519.Basepoint)
	if err != nil {
		return false, fmt.Errorf("calculate reality public key: %w", err)
	}

	privStr := base64.RawURLEncoding.EncodeToString(privKey)
	pubStr := base64.RawURLEncoding.EncodeToString(pubKey)

	if err := store.SetSetting("reality_private_key", privStr); err != nil {
		return false, fmt.Errorf("save reality private key: %w", err)
	}
	if err := store.SetSetting("reality_public_key", pubStr); err != nil {
		return false, fmt.Errorf("save reality public key: %w", err)
	}

	slog.Info("Generated Reality key pair")
	return true, nil
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

func get(store *db.Store, key string) string {
	value, err := store.GetSetting(key)
	if err != nil {
		return ""
	}
	return value
}

// mergeDefaultProtocols appends post-init default protocols to non-explicit lists; explicit lists stay alone (merging resurrected disabled protocols).
func mergeDefaultProtocols(store *db.Store) error {
	defaults := config.DefaultProtocolNames()

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
