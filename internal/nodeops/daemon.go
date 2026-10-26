package nodeops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// singboxSpec resolves the binary at each call: a Maintenance-installed copy on the volume wins over the image pin.
func singboxSpec(settings *config.Settings, opts setup.Options, logsDir string) supervisor.ProcessSpec {
	spec := supervisor.ProcessSpec{
		Name:    "sing-box",
		Command: updater.SingBoxBin(opts.InstallDir),
		Args:    []string{"run", "-c", filepath.Join(opts.InstallDir, "config.json")},
		LogPath: filepath.Join(logsDir, "sing-box.log"),
	}
	// Probe through the reality inbound: a hung sing-box still shows in the process table, only a TLS handshake tells a serving one.
	if slices.Contains(settings.Protocols, string(config.ProtoVLESSReality)) {
		spec.ProbeAddr = "127.0.0.1:8443"
		spec.ProbeSNI = settings.RealityServerName
	}
	return spec
}

// Daemon supervises sing-box, nginx, mtproto-proxy, tproxy-server and coredns, with ACME upkeep and the loopback probe and control endpoint.
func Daemon(ctx context.Context, store *db.Store, appCfg *config.AppConfig, opts setup.Options) error {
	settings, err := setup.Bootstrap(ctx, store, appCfg, opts)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	logsDir := filepath.Join(opts.DataDir, "logs")
	_ = os.MkdirAll(logsDir, 0o755)

	sup := supervisor.New()
	sup.AddProcess(singboxSpec(&settings, opts, logsDir))
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

	// rebuild is the one write path: commands reach it through /reload, the ACME loop after an issuance. It re-reads the settings, so the specs follow sync-ip, set-sni and set-protocols.
	var mu sync.Mutex
	rebuild := func(ctx context.Context, restart []string) error {
		mu.Lock()
		defer mu.Unlock()
		if err := setup.RebuildAll(ctx, store, sup, opts); err != nil {
			return err
		}
		s, err := setup.Load(store, opts)
		if err != nil {
			return err
		}
		for _, spec := range []supervisor.ProcessSpec{singboxSpec(&s, opts, logsDir), mtprotoSpec(&s, logsDir)} {
			if sup.Respec(spec) && !slices.Contains(restart, spec.Name) {
				restart = append(restart, spec.Name)
			}
		}
		for _, name := range restart {
			if err := sup.RestartProcess(name); err != nil {
				return err
			}
		}
		return nil
	}

	// The first pass renders only; a failed check keeps the configs on disk instead of taking the whole node down.
	if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
		slog.Error("Initial render failed, starting with the configs on disk", "error", err)
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

	go acme.Maintain(ctx, func() (*acme.Manager, error) {
		s, err := setup.Load(store, opts)
		if err != nil {
			return nil, err
		}
		return acme.New(s.Domain, s.Email, opts.CertsDir(), opts.WebRoot), nil
	}, func() {
		// The ssl server and the hysteria2 inbound exist only once a cert does.
		if err := rebuild(ctx, nil); err != nil {
			slog.Warn("Failed to rebuild configs after certificate issuance", "error", err)
		}
	})

	go tproxy.MaintainTelegramSecrets(ctx, func() {
		if err := sup.RestartProcess("mtproto-proxy"); err != nil {
			slog.Warn("Failed to restart mtproto-proxy after a Telegram config refresh", "error", err)
			return
		}
		slog.Info("Telegram MTProxy configuration refreshed, mtproto-proxy restarted")
	})

	go serveControl(ctx, sup, rebuild)

	slog.Info("Uncut Core daemon started successfully", "timezone", appCfg.Timezone, "domain", settings.Domain)
	<-ctx.Done()
	slog.Info("Shutting down daemon...")
	return nil
}

// serveControl is the loopback listener: /healthz for probes, POST /reload for the commands that changed the store.
func serveControl(ctx context.Context, sup *supervisor.Supervisor, rebuild func(context.Context, []string) error) {
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
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		var restart []string
		if v := r.URL.Query().Get("restart"); v != "" {
			restart = strings.Split(v, ",")
		}
		if err := rebuild(r.Context(), restart); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{
		Addr:        setup.DaemonAddr,
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
		// A reload runs the config checks and may wait out a child restart.
		WriteTimeout: 90 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	_ = server.ListenAndServe()
}
