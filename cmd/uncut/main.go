package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/tproxy"
	"github.com/rawizhere/uncut-core/internal/tui"
	"github.com/spf13/cobra"
)

var (
	dataDir    string
	installDir string
)

func main() {
	appCfg, err := config.LoadAppConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	config.InitLogger(appCfg.LogLevel)

	rootCmd := &cobra.Command{
		Use:   "uncut",
		Short: "Uncut Core Next-Generation Stealth VPN & Telegram Proxy Manager",
	}

	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "/opt/uncut/data", "Path to data directory")
	rootCmd.PersistentFlags().StringVar(&installDir, "install-dir", "/opt/sing-box", "Path to binary and config directory")

	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(menuCmd())
	rootCmd.AddCommand(addClientCmd())
	rootCmd.AddCommand(delClientCmd())
	rootCmd.AddCommand(listClientsCmd())
	rootCmd.AddCommand(syncIPCmd())
	rootCmd.AddCommand(renewCertCmd())
	rootCmd.AddCommand(changeDomainCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func initStore() (*db.Store, error) {
	_ = os.MkdirAll(dataDir, 0755)
	dbPath := filepath.Join(dataDir, "uncut.db")
	return db.New(dbPath)
}

func daemonCmd() *cobra.Command {
	var (
		zeroSSLEABKID  string
		zeroSSLEABHMAC string
	)

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run Uncut Core Supervisor Daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			appCfg, err := config.LoadAppConfig()
			if err != nil {
				return fmt.Errorf("load app config: %w", err)
			}
			if zeroSSLEABKID != "" {
				appCfg.ZeroSSLEABKID = zeroSSLEABKID
			}
			if zeroSSLEABHMAC != "" {
				appCfg.ZeroSSLEABHMAC = zeroSSLEABHMAC
			}

			opts := setup.DefaultOptions(dataDir, installDir)
			settings, err := setup.Ensure(cmd.Context(), store, appCfg, opts)
			if err != nil {
				return fmt.Errorf("ensure setup: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Supervisor setup
			logsDir := filepath.Join(dataDir, "logs")
			_ = os.MkdirAll(logsDir, 0o755)

			sup := supervisor.New()
			sup.AddProcess(supervisor.ProcessSpec{
				Name:    "sing-box",
				Command: "sing-box",
				Args:    []string{"run", "-c", filepath.Join(installDir, "config.json")},
				LogPath: filepath.Join(logsDir, "sing-box.log"),
			})
			sup.AddProcess(supervisor.ProcessSpec{
				Name:    "nginx",
				Command: "nginx",
				Args:    []string{"-g", "daemon off;"},
			})
			mtprotoArgs := []string{
				"-u", "nobody",
				"-p", "8888",
				"-H", "2398",
				"-S", settings.MTProtoRawSecret,
				"--aes-pwd", "/etc/mtproxy/proxy-secret",
				"-M", "1",
				"-C", "4096",
			}
			if settings.IP != "" {
				mtprotoArgs = append(mtprotoArgs, "--nat-info", fmt.Sprintf("127.0.0.1:%s", settings.IP))
			}
			mtprotoArgs = append(mtprotoArgs, "/etc/mtproxy/proxy-multi.conf")

			sup.AddProcess(supervisor.ProcessSpec{
				Name:    "mtproto-proxy",
				Command: "mtproto-proxy",
				Args:    mtprotoArgs,
				LogPath: filepath.Join(logsDir, "mtproto-proxy.log"),
			})
			sup.AddProcess(supervisor.ProcessSpec{
				Name:    "tproxy-server",
				Command: "tproxy-server",
				Args:    []string{"-config", filepath.Join(installDir, "tproxy/config.json")},
				LogPath: filepath.Join(logsDir, "tproxy-server.log"),
			})

			// ACME manager
			acmeMgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot, false, appCfg.ZeroSSLEABKID, appCfg.ZeroSSLEABHMAC)
			if _, err := acmeMgr.EnsureSelfSigned(); err != nil {
				slog.Warn("Failed to ensure self-signed certificate", "error", err)
			}

			if err := setup.RebuildAll(ctx, store, nil, opts); err != nil {
				return fmt.Errorf("rebuild configs: %w", err)
			}

			sup.StartAll()
			defer sup.StopAll()

			go acmeMgr.Maintain(ctx, func() {
				if err := sup.ReloadNginx(); err != nil {
					slog.Warn("Failed to reload nginx after cert renewal", "error", err)
				}
			})

			go tproxy.MaintainTelegramSecrets(ctx, func() {
				slog.Info("Telegram MTProxy configuration refreshed")
			})

			// Start internal healthcheck server
			go func() {
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
			}()

			slog.Info("Uncut Core daemon started successfully", "timezone", appCfg.Timezone, "domain", settings.Domain)
			<-ctx.Done()
			slog.Info("Shutting down daemon...")
			return nil
		},
	}

	cmd.Flags().StringVar(&zeroSSLEABKID, "zerossl-eab-kid", "", "ZeroSSL ACME EAB Key ID (forces ZeroSSL provider)")
	cmd.Flags().StringVar(&zeroSSLEABHMAC, "zerossl-eab-hmac", "", "ZeroSSL ACME EAB HMAC Key")
	return cmd
}

func menuCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "menu",
		Short: "Open interactive terminal administration menu",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			appCfg, _ := config.LoadAppConfig()
			opts := setup.DefaultOptions(dataDir, installDir)
			_, _ = setup.Ensure(cmd.Context(), store, appCfg, opts)

			m := tui.NewMenu(store, nil, installDir)
			return m.Run(context.Background())
		},
	}
}

func addClientCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new client",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				return fmt.Errorf("client name is required")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			appCfg, _ := config.LoadAppConfig()
			opts := setup.DefaultOptions(dataDir, installDir)
			_, err = setup.Ensure(cmd.Context(), store, appCfg, opts)
			if err != nil {
				return fmt.Errorf("ensure setup: %w", err)
			}

			client, err := core.AddClientWithDefaultProtocols(store, name)
			if err != nil {
				return err
			}

			if err := setup.RebuildAll(cmd.Context(), store, nil, opts); err != nil {
				return fmt.Errorf("rebuild configs: %w", err)
			}

			domain, _ := store.GetSetting("domain")
			subURL := core.GetSubscriptionURL(domain, client.SubHash)
			fmt.Printf("Client %s added.\nUUID: %s\nSubscription: %s\n", client.Name, client.UUID, subURL)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Client name")
	return cmd
}

func delClientCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "del",
		Short: "Delete a client by name or UUID",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				return fmt.Errorf("client name or UUID is required")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

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

			opts := setup.DefaultOptions(dataDir, installDir)
			if err := core.DeleteClient(store, opts.SubsDir, targetUUID); err != nil {
				return err
			}

			if err := setup.RebuildAll(cmd.Context(), store, nil, opts); err != nil {
				return fmt.Errorf("rebuild configs: %w", err)
			}

			fmt.Printf("Client %s removed.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Client name or UUID")
	return cmd
}

func listClientsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			appCfg, _ := config.LoadAppConfig()
			opts := setup.DefaultOptions(dataDir, installDir)
			_, _ = setup.Ensure(cmd.Context(), store, appCfg, opts)

			domain, _ := store.GetSetting("domain")
			clients, err := store.GetClients()
			if err != nil {
				return err
			}

			fmt.Println("Active Clients:")
			for _, c := range clients {
				subURL := core.GetSubscriptionURL(domain, c.SubHash)
				fmt.Printf("• %s\n  UUID: %s\n  Subscription: %s\n", c.Name, c.UUID, subURL)
			}
			return nil
		},
	}
}

func syncIPCmd() *cobra.Command {
	var forceIP string
	cmd := &cobra.Command{
		Use:   "sync-ip",
		Short: "Sync public IPv4 address",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			var newIP string
			if forceIP != "" {
				_ = store.SetSetting("server_ip", forceIP)
				newIP = forceIP
			} else {
				ip, _, err := core.SyncServerIP(context.Background(), store, nil, true)
				if err != nil {
					return err
				}
				newIP = ip
			}

			opts := setup.DefaultOptions(dataDir, installDir)
			if err := setup.RebuildAll(cmd.Context(), store, nil, opts); err != nil {
				return fmt.Errorf("rebuild configs: %w", err)
			}

			fmt.Printf("Server IP updated: %s\n", newIP)
			return nil
		},
	}
	cmd.Flags().StringVar(&forceIP, "force", "", "Force specific IPv4 address")
	return cmd
}

func renewCertCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "renew-cert",
		Short: "Force renewal of SSL/TLS certificate",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			settings, err := setup.Load(store, opts)
			if err != nil {
				return err
			}

			appCfg, err := config.LoadAppConfig()
			if err != nil {
				return err
			}

			acmeMgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot, false, appCfg.ZeroSSLEABKID, appCfg.ZeroSSLEABHMAC)
			fmt.Printf("Requesting certificate for %s...\n", settings.Domain)
			if err := acmeMgr.ForceRenew(); err != nil {
				return fmt.Errorf("certificate issuance failed: %w", err)
			}

			_ = exec.Command("nginx", "-s", "reload").Run()
			fmt.Println("SSL/TLS certificate renewed successfully and Nginx reloaded.")
			return nil
		},
	}
}

func changeDomainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "change-domain <domain>",
		Short: "Change server domain and regenerate certificates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			newDomain := strings.TrimSpace(strings.ToLower(args[0]))
			if newDomain == "" {
				return fmt.Errorf("domain cannot be empty")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if err := store.SetSetting("domain", newDomain); err != nil {
				return fmt.Errorf("update domain setting: %w", err)
			}

			opts := setup.DefaultOptions(dataDir, installDir)
			appCfg, err := config.LoadAppConfig()
			if err != nil {
				return err
			}

			acmeMgr := acme.New(newDomain, appCfg.Email, opts.CertsDir(), opts.WebRoot, false, appCfg.ZeroSSLEABKID, appCfg.ZeroSSLEABHMAC)
			fmt.Printf("Requesting TLS certificate for %s...\n", newDomain)
			if err := acmeMgr.ForceRenew(); err != nil {
				slog.Warn("TLS certificate request failed", "error", err)
			}

			if err := setup.RebuildAll(cmd.Context(), store, nil, opts); err != nil {
				return fmt.Errorf("rebuild configs: %w", err)
			}

			_ = exec.Command("nginx", "-s", "reload").Run()
			fmt.Printf("Domain successfully changed to %s and all subscriptions updated.\n", newDomain)
			return nil
		},
	}
}
