// Package nodeops holds the node commands' business logic as plain functions over (store, options); cmd/uncut keeps only flag parsing and output.
package nodeops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/links"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/transport"
	"github.com/rawizhere/uncut-core/internal/updater"
)

// ClientView is the machine-readable client record `raw add --json` and `raw list --json` print.
type ClientView struct {
	Name         string `json:"name"`
	UUID         string `json:"uuid"`
	SubHash      string `json:"sub_hash"`
	Subscription string `json:"subscription"`
	// Protocols mirrors the stored allowlist; explicit false means inherit.
	Protocols         []string `json:"protocols,omitempty"`
	ProtocolsExplicit bool     `json:"protocols_explicit,omitempty"`
}

// NewClientView renders one client; the subscription URL comes from the transport.
func NewClientView(c config.Client, domain string, l *transport.Transport) ClientView {
	return ClientView{
		Name:              c.Name,
		UUID:              c.UUID,
		SubHash:           c.SubHash,
		Subscription:      l.SubURL(domain, c.SubHash),
		Protocols:         c.Protocols,
		ProtocolsExplicit: c.ProtocolsExplicit,
	}
}

// ClientList is the payload of `raw list --json`.
type ClientList struct {
	Clients          []ClientView `json:"clients"`
	Domain           string       `json:"domain"`
	MTProtoRawSecret string       `json:"mtproto_raw_secret"`
}

// Info is the payload of `raw info --json`.
type Info struct {
	Version string `json:"version"`
	Tag     string `json:"tag"`
	Domain  string `json:"domain"`
	// TransportRev fingerprints the paths; change it and every link re-issues.
	TransportRev string   `json:"transport_rev"`
	Protocols    []string `json:"protocols"`
	Clients      int      `json:"clients"`
	CertExpires  string   `json:"cert_expires"`
	// BridgeURL is the masked Telegram web-proxy page; empty without a secret.
	BridgeURL string `json:"bridge_url"`
	// WebProxyURL is the tg://webproxy link carrying the marked secret.
	WebProxyURL string `json:"webproxy_url"`
	// MTProtoTLSDomain is the FakeTLS impersonation target; empty = branch off.
	MTProtoTLSDomain string `json:"mtproto_tls_domain"`
	// MTProtoURL is the t.me FakeTLS proxy link; empty while the branch is off.
	MTProtoURL string `json:"mtproto_url"`
	// SingBoxVersion is the installed sing-box artifact version.
	SingBoxVersion string `json:"singbox_version"`
	// DohURL is the private DoH endpoint behind nginx; same secret family as the subscription URL.
	DohURL string `json:"doh_url"`
}

// AddClient creates the client and rebuilds.
func AddClient(ctx context.Context, store *db.Store, opts setup.Options, name string, protos []string) (*ClientView, error) {
	if name == "" {
		return nil, errors.New("client name is required")
	}

	appCfg, _ := config.LoadAppConfig()
	settings, err := setup.Ensure(ctx, store, appCfg, opts)
	if err != nil {
		return nil, fmt.Errorf("ensure setup: %w", err)
	}

	chosen := protos
	if len(chosen) == 0 {
		chosen = core.DefaultProtocols()
	}
	client, err := core.AddClient(store, name, chosen)
	if err != nil {
		return nil, err
	}
	if len(protos) > 0 {
		// An operator-passed --protocols at add time is an explicit allowlist, not a creation snapshot.
		client.ProtocolsExplicit = true
		if err := store.AddClient(*client); err != nil {
			return nil, fmt.Errorf("update client: %w", err)
		}
	}

	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return nil, fmt.Errorf("rebuild configs: %w", err)
	}

	domain, _ := store.GetSetting("domain")
	view := NewClientView(*client, domain, settings.Transport)
	return &view, nil
}

// RotateSub replaces the client's subscription token and rebuilds; the old subscription URL dies.
func RotateSub(ctx context.Context, store *db.Store, opts setup.Options, name string) (*ClientView, error) {
	client, err := findClient(store, name)
	if err != nil {
		return nil, err
	}

	appCfg, _ := config.LoadAppConfig()
	settings, err := setup.Ensure(ctx, store, appCfg, opts)
	if err != nil {
		return nil, fmt.Errorf("ensure setup: %w", err)
	}

	client, err = core.RotateClientSub(store, opts.SubsDir, client.UUID)
	if err != nil {
		return nil, err
	}

	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return nil, fmt.Errorf("rebuild configs: %w", err)
	}

	domain, _ := store.GetSetting("domain")
	view := NewClientView(*client, domain, settings.Transport)
	return &view, nil
}

// findClient resolves a client by exact name.
func findClient(store *db.Store, name string) (*config.Client, error) {
	clients, err := store.GetClients()
	if err != nil {
		return nil, err
	}
	for i := range clients {
		if clients[i].Name == name {
			return &clients[i], nil
		}
	}
	return nil, fmt.Errorf("client not found: %s", name)
}

// SetClientProtocols makes an explicit allowlist: off stays off for the client.
func SetClientProtocols(ctx context.Context, store *db.Store, opts setup.Options, name string, protocols []string) (*ClientView, error) {
	sanitized := make([]string, 0, len(protocols))
	for _, p := range protocols {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !config.IsValidProtocol(p) {
			return nil, fmt.Errorf("unknown protocol %q", p)
		}
		sanitized = append(sanitized, p)
	}
	if len(sanitized) == 0 {
		return nil, errors.New("at least one protocol is required; --inherit goes back to the server set")
	}
	client, err := findClient(store, name)
	if err != nil {
		return nil, err
	}
	if _, err := core.UpdateClientProtocols(store, client.UUID, sanitized); err != nil {
		return nil, err
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return nil, fmt.Errorf("rebuild configs: %w", err)
	}
	return clientViewAfter(ctx, store, opts, client.UUID)
}

// InheritClientProtocols clears the explicit choice: the server's set flows again.
func InheritClientProtocols(ctx context.Context, store *db.Store, opts setup.Options, name string) (*ClientView, error) {
	client, err := findClient(store, name)
	if err != nil {
		return nil, err
	}
	if _, err := core.InheritClientProtocols(store, client.UUID); err != nil {
		return nil, err
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return nil, fmt.Errorf("rebuild configs: %w", err)
	}
	return clientViewAfter(ctx, store, opts, client.UUID)
}

// SetMTProtoTLSDomain enables FakeTLS with a third-party domain to impersonate (unique SNI, must answer 443); empty disables the branch.
func SetMTProtoTLSDomain(ctx context.Context, store *db.Store, opts setup.Options, domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		if err := store.SetSetting("mtproto_tls_domain", ""); err != nil {
			return "", err
		}
		if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
			return "", fmt.Errorf("rebuild configs: %w", err)
		}
		return "", nil
	}

	settings, err := setup.Load(store, opts)
	if err != nil {
		return "", err
	}
	if domain == strings.ToLower(strings.TrimSpace(settings.Domain)) {
		return "", fmt.Errorf("tls domain must not equal the node domain %q", settings.Domain)
	}
	if domain == strings.ToLower(strings.TrimSpace(settings.RealityServerName)) {
		return "", fmt.Errorf("tls domain must not equal the reality SNI %q: one SNI can hold only one map key", settings.RealityServerName)
	}
	ips, lookupErr := net.LookupIP(domain)
	if lookupErr != nil || len(ips) == 0 {
		return "", fmt.Errorf("tls domain %q does not resolve: %w", domain, lookupErr)
	}
	conn, dialErr := net.DialTimeout("tcp", net.JoinHostPort(domain, "443"), 5*time.Second)
	if dialErr != nil {
		return "", fmt.Errorf("tls domain %q is not reachable on 443 from here: %w", domain, dialErr)
	}
	_ = conn.Close()

	if err := store.SetSetting("mtproto_tls_domain", domain); err != nil {
		return "", err
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return "", fmt.Errorf("rebuild configs: %w", err)
	}
	return domain, nil
}

// SetRealitySNI is transport data: it changes the rev, every subscription re-issues, and the host must differ from the FakeTLS domain.
func SetRealitySNI(ctx context.Context, store *db.Store, opts setup.Options, host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", errors.New("reality SNI is required: the reality handshake needs a face")
	}
	settings, err := setup.Load(store, opts)
	if err != nil {
		return "", err
	}
	if host == strings.ToLower(strings.TrimSpace(settings.MTProtoTLSDomain)) {
		return "", fmt.Errorf("reality SNI must not equal the mtproto tls domain %q: one SNI can hold only one map key", settings.MTProtoTLSDomain)
	}
	t := *settings.Transport
	t.RealityServerName = host
	if err := t.Validate(); err != nil {
		return "", err
	}
	settings.Transport.RealityServerName = host
	if err := setup.SaveTransport(store, settings.Transport); err != nil {
		return "", err
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return "", fmt.Errorf("rebuild configs: %w", err)
	}
	return settings.Transport.Rev(), nil
}

func clientViewAfter(ctx context.Context, store *db.Store, opts setup.Options, uuid string) (*ClientView, error) {
	client, err := store.GetClientByUUID(uuid)
	if err != nil {
		return nil, err
	}
	settings, err := setup.Load(store, opts)
	if err != nil {
		return nil, err
	}
	view := NewClientView(*client, settings.Domain, settings.Transport)
	return &view, nil
}

// DeleteClient removes the client by name or UUID, including its subscription files, and rebuilds the configs.
func DeleteClient(ctx context.Context, store *db.Store, opts setup.Options, name string) error {
	if name == "" {
		return errors.New("client name or UUID is required")
	}

	clients, _ := store.GetClients()
	var targetUUID string
	for _, c := range clients {
		if c.Name == name || c.UUID == name {
			targetUUID = c.UUID
			break
		}
	}
	if targetUUID == "" {
		return fmt.Errorf("client %s not found", name)
	}

	if err := core.DeleteClient(store, opts.SubsDir, targetUUID); err != nil {
		return err
	}
	return setup.RebuildAll(ctx, store, nil, opts)
}

// ListClients returns the node's clients plus the fqdn and raw secret link builders need.
func ListClients(ctx context.Context, store *db.Store, opts setup.Options) (*ClientList, error) {
	appCfg, _ := config.LoadAppConfig()
	settings, _ := setup.Ensure(ctx, store, appCfg, opts)

	domain, _ := store.GetSetting("domain")
	clients, err := store.GetClients()
	if err != nil {
		return nil, err
	}

	out := &ClientList{Domain: domain}
	for _, c := range clients {
		out.Clients = append(out.Clients, NewClientView(c, domain, settings.Transport))
	}
	out.MTProtoRawSecret, _ = store.GetSetting("mtproto_raw_secret")
	return out, nil
}

// SyncIP stores the node's public IPv4 (or a forced one) and rebuilds.
func SyncIP(ctx context.Context, store *db.Store, opts setup.Options, forceIP string) (string, error) {
	var newIP string
	if forceIP != "" {
		if err := store.SetSetting("server_ip", forceIP); err != nil {
			return "", err
		}
		newIP = forceIP
	} else {
		ip, _, err := core.SyncServerIP(ctx, store, nil, true)
		if err != nil {
			return "", err
		}
		newIP = ip
	}

	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return "", fmt.Errorf("rebuild configs: %w", err)
	}
	return newIP, nil
}

// RenewCert forces issuance and reloads nginx + sing-box (Hysteria2).
func RenewCert(store *db.Store, opts setup.Options) error {
	settings, err := setup.Load(store, opts)
	if err != nil {
		return err
	}

	mgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot)
	if err := mgr.ForceRenew(); err != nil {
		return fmt.Errorf("certificate issuance failed: %w", err)
	}

	_ = exec.Command("nginx", "-s", "reload").Run()
	_ = exec.Command("pkill", "-HUP", "sing-box").Run()
	return nil
}

// ChangeDomain stores the new fqdn, re-issues the cert, rebuilds all.
func ChangeDomain(ctx context.Context, store *db.Store, opts setup.Options, newDomain string) error {
	newDomain = strings.TrimSpace(strings.ToLower(newDomain))
	if newDomain == "" {
		return errors.New("domain cannot be empty")
	}

	if err := store.SetSetting("domain", newDomain); err != nil {
		return fmt.Errorf("update domain setting: %w", err)
	}

	appCfg, err := config.LoadAppConfig()
	if err != nil {
		return err
	}

	mgr := acme.New(newDomain, appCfg.Email, opts.CertsDir(), opts.WebRoot)
	if err := mgr.ForceRenew(); err != nil {
		// Maintain retries in the background; a daemon restart re-runs issuance.
		slog.Warn("TLS certificate request failed", "error", err)
	}

	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return fmt.Errorf("rebuild configs: %w", err)
	}

	_ = exec.Command("nginx", "-s", "reload").Run()
	return nil
}

// Info reports the node's state: version, tag, transport revision, client count and certificate expiry.
func GetInfo(store *db.Store, opts setup.Options) (*Info, error) {
	settings, err := setup.Load(store, opts)
	if err != nil {
		return nil, err
	}

	clients, err := store.GetClients()
	if err != nil {
		return nil, err
	}

	certExpires := ""
	mgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot)
	if expires, err := mgr.Expires(); err == nil {
		certExpires = expires.UTC().Format(time.RFC3339)
	}

	bridgeURL, webproxyURL, mtprotoURL := "", "", ""
	if settings.MTProtoRawSecret != "" {
		bridgeURL = links.BridgeURL(settings.Domain, settings.MTProtoRawSecret)
		webproxyURL = links.WebProxyURL(settings.Domain, settings.MTProtoRawSecret)
		if settings.MTProtoTLSDomain != "" {
			mtprotoURL = links.TMeProxyURL(settings.Domain,
				links.MTProtoFakeTLSSecret(settings.MTProtoRawSecret, settings.MTProtoTLSDomain))
		}
	}

	return &Info{
		Version:          config.Version,
		Tag:              settings.Tag,
		Domain:           settings.Domain,
		TransportRev:     settings.Transport.Rev(),
		Protocols:        settings.Protocols,
		Clients:          len(clients),
		CertExpires:      certExpires,
		BridgeURL:        bridgeURL,
		WebProxyURL:      webproxyURL,
		MTProtoTLSDomain: settings.MTProtoTLSDomain,
		MTProtoURL:       mtprotoURL,
		SingBoxVersion:   updater.CurrentVersion(opts.InstallDir),
		DohURL:           "https://" + settings.Domain + settings.Transport.DoHPath(),
	}, nil
}

// RotatePaths regenerates the transport and rebuilds; every issued link dies.
func RotatePaths(ctx context.Context, store *db.Store, opts setup.Options) (string, error) {
	t, err := transport.Generate()
	if err != nil {
		return "", fmt.Errorf("generate transport: %w", err)
	}
	if err := setup.SaveTransport(store, t); err != nil {
		return "", err
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return "", fmt.Errorf("rebuild configs: %w", err)
	}
	return t.Rev(), nil
}

// SetProtocols replaces the server set; the explicit marker keeps the next daemon start from merging defaults back (which resurrected disabled ones).
func SetProtocols(ctx context.Context, store *db.Store, opts setup.Options, protocols []string) ([]string, error) {
	var resolved []string
	for _, p := range protocols {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !config.IsValidProtocol(p) {
			return nil, fmt.Errorf("unknown protocol %q", p)
		}
		resolved = append(resolved, p)
	}
	if len(resolved) == 0 {
		return nil, errors.New("at least one protocol is required")
	}

	if err := store.SetSetting("protocols", strings.Join(resolved, ",")); err != nil {
		return nil, fmt.Errorf("save protocols: %w", err)
	}
	if err := store.SetSetting("protocols_explicit", "true"); err != nil {
		return nil, fmt.Errorf("mark protocols explicit: %w", err)
	}
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return nil, fmt.Errorf("rebuild configs: %w", err)
	}
	return resolved, nil
}

// SetCert installs a custom certificate and reloads nginx + sing-box.
func SetCert(store *db.Store, opts setup.Options, certData, keyData []byte) (string, error) {
	domain, err := store.GetSetting("domain")
	if err != nil || domain == "" {
		return "", fmt.Errorf("domain not found in settings: %w", err)
	}

	certsDir := opts.CertsDir()
	_ = os.MkdirAll(certsDir, 0o700)

	certPath := filepath.Join(certsDir, domain+".crt")
	keyPath := filepath.Join(certsDir, domain+".key")

	if err := os.WriteFile(certPath, certData, 0o644); err != nil {
		return "", fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyData, 0o600); err != nil {
		return "", fmt.Errorf("write key: %w", err)
	}

	_ = exec.Command("nginx", "-s", "reload").Run()
	_ = exec.Command("pkill", "-HUP", "sing-box").Run()
	return domain, nil
}
