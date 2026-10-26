package nodeops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/tproxy"
	"github.com/rawizhere/uncut-core/internal/updater"
)

// mtprotoSpec builds the plain mtproto-proxy behind tproxy; only the bridge reaches 2398, so it takes only the webproxy secret.
func mtprotoSpec(settings *config.Settings, logsDir string) supervisor.ProcessSpec {
	args := []string{
		"-u", "nobody",
		"-p", "8888",
		"-H", "2398",
		"-S", settings.WebProxySecret,
		"--aes-pwd", "/etc/mtproxy/proxy-secret",
		"-M", "1",
		"-C", "4096",
	}
	if settings.IP != "" {
		args = append(args, "--nat-info", fmt.Sprintf("127.0.0.1:%s", settings.IP))
	}
	args = append(args, "/etc/mtproxy/proxy-multi.conf")
	return supervisor.ProcessSpec{
		Name:    "mtproto-proxy",
		Command: "mtproto-proxy",
		Args:    args,
		LogPath: filepath.Join(logsDir, "mtproto-proxy.log"),
	}
}

// Daemon supervises sing-box, nginx, mtproto-proxy and tproxy-server, with ACME upkeep and a loopback probe endpoint.
func Daemon(ctx context.Context, store *db.Store, appCfg *config.AppConfig, opts setup.Options) error {

	settings, err := setup.Ensure(ctx, store, appCfg, opts)
	if err != nil {
		return fmt.Errorf("ensure setup: %w", err)
	}

	logsDir := filepath.Join(opts.DataDir, "logs")
	_ = os.MkdirAll(logsDir, 0o755)

	sup := supervisor.New()
	sbSpec := supervisor.ProcessSpec{
		Name:    "sing-box",
		Command: updater.SingBoxBin(opts.InstallDir),
		Args:    []string{"run", "-c", filepath.Join(opts.InstallDir, "config.json")},
		LogPath: filepath.Join(logsDir, "sing-box.log"),
	}
	// Probe through the reality inbound: a hung sing-box still shows in the process table, only a TLS handshake tells a serving one.
	for _, p := range settings.Protocols {
		if p == string(config.ProtoVLESSReality) {
			sbSpec.ProbeAddr = "127.0.0.1:8443"
			sbSpec.ProbeSNI = settings.RealityServerName
			break
		}
	}
	sup.AddProcess(sbSpec)
	sup.AddProcess(supervisor.ProcessSpec{
		Name:    "nginx",
		Command: "nginx",
		Args:    []string{"-g", "daemon off;"},
	})
	// The FakeTLS branch is sing-box's mtproxy inbound, applied by the sing-box reload; this instance only serves the bridge.
	sup.AddProcess(mtprotoSpec(&settings, logsDir))
	sup.AddProcess(supervisor.ProcessSpec{
		Name:    "tproxy-server",
		Command: "tproxy-server",
		Args:    []string{"-config", filepath.Join(opts.InstallDir, "tproxy/config.json")},
		LogPath: filepath.Join(logsDir, "tproxy-server.log"),
	})
	// CoreDNS serves the private DoH endpoint on loopback; nginx is the only client, so the endpoint adds no port on the node's face.
	sup.AddProcess(supervisor.ProcessSpec{
		Name:    "coredns",
		Command: "coredns",
		Args:    []string{"-conf", "/etc/coredns/Corefile"},
		LogPath: filepath.Join(logsDir, "coredns.log"),
	})

	acmeMgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot)
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		return fmt.Errorf("rebuild configs: %w", err)
	}

	sup.StartAll()
	defer sup.StopAll()

	// Bridge failure modes look identical from outside; this logs which.
	go func() {
		if err := tproxy.CheckBridge(ctx, settings.Domain, settings.WebProxySecret); err != nil {
			slog.Warn("Telegram bridge self-check failed", "error", err)
			return
		}
		if err := tproxy.CheckBackend(ctx, ""); err != nil {
			slog.Warn("Telegram bridge serves its page but the backend is down", "error", err)
			return
		}
		slog.Info("Telegram bridge self-check passed", "domain", settings.Domain)
	}()

	go acmeMgr.Maintain(ctx, func() {
		// Re-render (the ssl block exists only once a cert does, Hysteria2 re-reads) — RebuildAll reloads both consumers.
		if err := setup.RebuildAll(ctx, store, sup, opts); err != nil {
			slog.Warn("Failed to rebuild configs after cert renewal", "error", err)
		}
	})

	go tproxy.MaintainTelegramSecrets(ctx, func() {
		if err := sup.RestartProcess("mtproto-proxy"); err != nil {
			slog.Warn("Failed to restart mtproto-proxy after a Telegram config refresh", "error", err)
			return
		}
		slog.Info("Telegram MTProxy configuration refreshed, mtproto-proxy restarted")
	})

	// change-domain runs in another process and cannot restart this daemon's tproxy child; watch the files and restart it when they actually change.
	go watchTproxyConfig(ctx, sup, opts.InstallDir)

	go serveHealth(ctx, sup)

	slog.Info("Uncut Core daemon started successfully", "timezone", appCfg.Timezone, "domain", settings.Domain)
	<-ctx.Done()
	slog.Info("Shutting down daemon...")
	return nil
}

// serveHealth exposes aggregate process status on 127.0.0.1:8088 for probes.
func serveHealth(ctx context.Context, sup *supervisor.Supervisor) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		status := sup.GetStatus()
		allHealthy := true
		for _, running := range status {
			if !running {
				allHealthy = false
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if !allHealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"healthy":   allHealthy,
			"processes": status,
			"time":      time.Now().Format(time.RFC3339),
		})
	})
	server := &http.Server{
		Addr:         "127.0.0.1:8088",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	_ = server.ListenAndServe()
}

// watchTproxyConfig restarts tproxy-server when its config files change; tproxy reads them only at startup.
func watchTproxyConfig(ctx context.Context, sup *supervisor.Supervisor, installDir string) {
	cfgPath := filepath.Join(installDir, "tproxy", "config.json")
	profilesPath := filepath.Join(installDir, "tproxy", "profiles.json")
	hash := func() string {
		h := sha256.New()
		for _, p := range []string{cfgPath, profilesPath} {
			data, err := os.ReadFile(p)
			if err != nil {
				return ""
			}
			h.Write(data)
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}

	last := hash()
	if last == "" {
		return // files not there yet: nothing to watch
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := hash()
			if cur == "" || cur == last {
				continue
			}
			last = cur
			if err := sup.RestartProcess("tproxy-server"); err != nil {
				slog.Warn("Failed to restart tproxy after config change", "error", err)
				continue
			}
			slog.Info("Restarted tproxy-server: its configuration changed on disk")
		}
	}
}
