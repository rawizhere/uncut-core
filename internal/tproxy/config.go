package tproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rawizhere/uncut-core/internal/config"
)

type Profile struct {
	Name        string `json:"name"`
	Secret      string `json:"secret"`
	Backend     string `json:"backend"`
	CarrierMode string `json:"carrier_mode"`
}

type ProfilesConfig struct {
	Profiles []Profile `json:"profiles"`
}

// Limits pins upstream defaults; the bridge page reads the batch size back.
type Limits struct {
	MaxHeaderBytes            int `json:"max_header_bytes"`
	MaxBodyBytes              int `json:"max_body_bytes"`
	MaxFramePayload           int `json:"max_frame_payload"`
	CarrierBatchBytes         int `json:"carrier_batch_bytes"`
	MaxStreamsPerSession      int `json:"max_streams_per_session"`
	MaxClosedStreamIDs        int `json:"max_closed_stream_ids"`
	MaxPendingPerSession      int `json:"max_pending_per_session"`
	MaxPendingGlobal          int `json:"max_pending_global"`
	MaxPendingItemsPerSession int `json:"max_pending_items_per_session"`
	MaxPendingItemsGlobal     int `json:"max_pending_items_global"`
	MaxSessionsPerIP          int `json:"max_sessions_per_ip"`
	MaxSessionsGlobal         int `json:"max_sessions_global"`
	MaxStreamsGlobal          int `json:"max_streams_global"`
	MaxBackendDialsInFlight   int `json:"max_backend_dials_in_flight"`
	NewSessionsPerMinute      int `json:"new_sessions_per_minute"`
	NewSessionsBurst          int `json:"new_sessions_burst"`
	NewStreamsPerMinute       int `json:"new_streams_per_minute"`
	NewStreamsBurst           int `json:"new_streams_burst"`
	MaxBootstrapsPerIP        int `json:"max_bootstraps_per_ip"`
	MaxBootstrapsGlobal       int `json:"max_bootstraps_global"`
	NewBootstrapsPerMinute    int `json:"new_bootstraps_per_minute"`
	NewBootstrapsBurst        int `json:"new_bootstraps_burst"`
	MaxProfiles               int `json:"max_profiles"`
}

type Timeouts struct {
	BackendDial       string `json:"backend_dial"`
	LongPoll          string `json:"long_poll"`
	ReconnectGrace    string `json:"reconnect_grace"`
	BootstrapLifetime string `json:"bootstrap_lifetime"`
	ReadHeader        string `json:"read_header"`
	Idle              string `json:"idle"`
	Shutdown          string `json:"shutdown"`
}

type Config struct {
	PublicHostname string   `json:"public_hostname"`
	Listen         string   `json:"listen"`
	AdminListen    string   `json:"admin_listen"`
	PublicDir      string   `json:"public_dir"`
	ProfilesFile   string   `json:"profiles_file"`
	EnablePprof    bool     `json:"enable_pprof"`
	Limits         Limits   `json:"limits"`
	Timeouts       Timeouts `json:"timeouts"`
}

func defaultLimits() Limits {
	return Limits{
		MaxHeaderBytes:            16 * 1024,
		MaxBodyBytes:              2 * 1024 * 1024,
		MaxFramePayload:           1024 * 1024,
		CarrierBatchBytes:         2 * 1024 * 1024,
		MaxStreamsPerSession:      128,
		MaxClosedStreamIDs:        4096,
		MaxPendingPerSession:      32 * 1024 * 1024,
		MaxPendingGlobal:          512 * 1024 * 1024,
		MaxPendingItemsPerSession: 16 * 1024,
		MaxPendingItemsGlobal:     256 * 1024,
		MaxSessionsPerIP:          0,
		MaxSessionsGlobal:         128,
		MaxStreamsGlobal:          4096,
		MaxBackendDialsInFlight:   256,
		NewSessionsPerMinute:      600,
		NewSessionsBurst:          128,
		NewStreamsPerMinute:       6000,
		NewStreamsBurst:           512,
		MaxBootstrapsPerIP:        0,
		MaxBootstrapsGlobal:       512,
		NewBootstrapsPerMinute:    1200,
		NewBootstrapsBurst:        256,
		MaxProfiles:               32,
	}
}

func defaultTimeouts() Timeouts {
	return Timeouts{
		BackendDial:       "5s",
		LongPoll:          "25s",
		ReconnectGrace:    "2m",
		BootstrapLifetime: "2m",
		ReadHeader:        "10s",
		Idle:              "75s",
		Shutdown:          "15s",
	}
}

func ensureSiteIndex(publicDir string) error {
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		return fmt.Errorf("create public dir: %w", err)
	}

	indexPath := filepath.Join(publicDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		return nil
	}

	indexHTML := `<!DOCTYPE html><html><head><meta name="robots" content="noindex"></head><body></body></html>`
	return config.WriteFileAtomic(indexPath, []byte(indexHTML), 0o644)
}

// DefaultPublicDir is where the operator-owned public site lives on a node.
const DefaultPublicDir = "/srv/tproxy-site"

// DefaultBackend is the loopback mtproto-proxy only tproxy itself may reach.
const DefaultBackend = "127.0.0.1:2398"

// Files renders the tproxy config and profiles by path; tproxy reads them only at startup, so the caller restarts it when they change.
func Files(installDir, domain, secret, publicDir string) (map[string][]byte, error) {
	tproxyDir := filepath.Join(installDir, "tproxy")
	if err := os.MkdirAll(tproxyDir, 0o700); err != nil {
		return nil, fmt.Errorf("create tproxy dir: %w", err)
	}

	if publicDir == "" {
		publicDir = DefaultPublicDir
	}

	profilesFile := filepath.Join(tproxyDir, "profiles.json")
	configFile := filepath.Join(tproxyDir, "config.json")

	// tproxy-server refuses to start without an index in public_dir.
	if err := ensureSiteIndex(publicDir); err != nil {
		return nil, fmt.Errorf("ensure tproxy site index: %w", err)
	}

	profiles := ProfilesConfig{
		Profiles: []Profile{
			{
				Name:        "default",
				Secret:      secret,
				Backend:     DefaultBackend,
				CarrierMode: "https",
			},
		},
	}

	cfg := Config{
		PublicHostname: domain,
		Listen:         net.JoinHostPort("127.0.0.1", config.DefaultTelegramProxyPort),
		AdminListen:    "127.0.0.1:8081",
		PublicDir:      publicDir,
		ProfilesFile:   profilesFile,
		EnablePprof:    false,
		Limits:         defaultLimits(),
		Timeouts:       defaultTimeouts(),
	}

	profilesData, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal profiles: %w", err)
	}

	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return map[string][]byte{profilesFile: profilesData, configFile: cfgData}, nil
}

// MaintainTelegramSecrets refreshes the middle-proxy secret and config now and daily; onChange runs only when a file actually changed, since mtproto-proxy reads them once at start.
func MaintainTelegramSecrets(ctx context.Context, onChange func()) {
	update := func() {
		changed := false
		for _, f := range []struct{ url, path, marker string }{
			{"https://core.telegram.org/getProxySecret", "/etc/mtproxy/proxy-secret", ""},
			{"https://core.telegram.org/getProxyConfig", "/etc/mtproxy/proxy-multi.conf", "proxy_for"},
		} {
			c, err := refreshFile(ctx, f.url, f.path, f.marker)
			if err != nil {
				slog.Warn("Telegram proxy file refresh failed, keeping the current one", "url", f.url, "error", err)
				continue
			}
			changed = changed || c
		}
		if changed && onChange != nil {
			onChange()
		}
	}

	update()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

// refreshFile replaces path with the body at url when it differs; a body without marker is an error page, not a config.
func refreshFile(ctx context.Context, url, path, marker string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	if len(data) == 0 || !bytes.Contains(data, []byte(marker)) {
		return false, errors.New("unexpected body")
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, config.WriteFileAtomic(path, data, 0o640)
}
