package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/nodeops"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/tui"
	"github.com/rawizhere/uncut-core/internal/updater"
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
		Use:   "raw",
		Short: "Uncut Core Next-Generation Stealth VPN & Telegram Proxy Manager",
	}

	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "/opt/uncut/data", "Path to data directory")
	rootCmd.PersistentFlags().StringVar(&installDir, "install-dir", "/opt/sing-box", "Path to binary and config directory")

	// A bare invocation is the terminal menu: the interactive entry point is the CLI's front door.
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return openMenu(cmd)
	}

	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(singboxVersionCmd())
	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(menuCmd())
	rootCmd.AddCommand(addClientCmd())
	rootCmd.AddCommand(delClientCmd())
	rootCmd.AddCommand(listClientsCmd())
	rootCmd.AddCommand(syncIPCmd())
	rootCmd.AddCommand(renewCertCmd())
	rootCmd.AddCommand(changeDomainCmd())
	rootCmd.AddCommand(infoCmd())
	rootCmd.AddCommand(rotatePathsCmd())
	rootCmd.AddCommand(rotateSubCmd())
	rootCmd.AddCommand(setProtocolsCmd())
	rootCmd.AddCommand(setClientProtocolsCmd())
	rootCmd.AddCommand(setMTProtoTLSCmd())
	rootCmd.AddCommand(setSNICmd())
	rootCmd.AddCommand(setCertCmd())

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
	var ()

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

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			opts := setup.DefaultOptions(dataDir, installDir)
			return nodeops.Daemon(ctx, store, appCfg, opts)
		},
	}

	return cmd
}

func singboxVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "singbox-version <version>",
		Short: "Install a Sing-box Extended release by version, without a GitHub API fetch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimPrefix(args[0], "v")
			ctx := cmd.Context()
			fmt.Printf("Downloading and installing %s...\n", target)
			if err := updater.InstallSingboxVersion(ctx, target, filepath.Join(installDir, "bin", "sing-box")); err != nil {
				return err
			}
			fmt.Println("Sing-box installed; the running instance restarts on the next reload.")
			return nil
		},
	}
}

func menuCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "menu",
		Short: "Open interactive terminal administration menu",
		RunE: func(cmd *cobra.Command, args []string) error {
			return openMenu(cmd)
		},
	}
}

func openMenu(cmd *cobra.Command) error {
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
}

func addClientCmd() *cobra.Command {
	var (
		name   string
		asJSON bool
		protos []string
	)
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

			opts := setup.DefaultOptions(dataDir, installDir)
			view, err := nodeops.AddClient(cmd.Context(), store, opts, name, protos)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(view)
			}

			fmt.Printf("Client %s added.\nUUID: %s\nSubscription: %s\n", view.Name, view.UUID, view.Subscription)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Client name")
	cmd.Flags().StringSliceVar(&protos, "protocols", nil, "Protocols to enable, defaults to the standard set")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func rotateSubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate-sub <name>",
		Short: "Regenerate a client's subscription token; the old subscription URL dies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			view, err := nodeops.RotateSub(cmd.Context(), store, opts, args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Subscription rotated for %s.\nNew URL: %s\nOld URL no longer works.\n", view.Name, view.Subscription)
			return nil
		},
	}
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

			opts := setup.DefaultOptions(dataDir, installDir)
			if err := nodeops.DeleteClient(cmd.Context(), store, opts, name); err != nil {
				return err
			}

			fmt.Printf("Client %s removed.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Client name or UUID")
	return cmd
}

func listClientsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all active clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			list, err := nodeops.ListClients(cmd.Context(), store, opts)
			if err != nil {
				return err
			}

			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(list)
			}

			fmt.Println("Active Clients:")
			for _, c := range list.Clients {
				protos := "inherit server set"
				if c.ProtocolsExplicit {
					protos = strings.Join(c.Protocols, ", ")
				}
				fmt.Printf("• %s\n  UUID: %s\n  Protocols: %s\n  Subscription: %s\n", c.Name, c.UUID, protos, c.Subscription)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
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

			opts := setup.DefaultOptions(dataDir, installDir)
			newIP, err := nodeops.SyncIP(cmd.Context(), store, opts, forceIP)
			if err != nil {
				return err
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
			if err := nodeops.RenewCert(store, opts); err != nil {
				return err
			}
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
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			if err := nodeops.ChangeDomain(cmd.Context(), store, opts, args[0]); err != nil {
				return err
			}
			fmt.Printf("Domain successfully changed to %s and all subscriptions updated.\n", strings.TrimSpace(strings.ToLower(args[0])))
			return nil
		},
	}
}

func infoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Print node state: region, transport revision, certificate expiry",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			info, err := nodeops.GetInfo(store, opts)
			if err != nil {
				return err
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}

			fmt.Printf("version:       %s\n", info.Version)
			fmt.Printf("tag:           %s\n", info.Tag)
			fmt.Printf("domain:        %s\n", info.Domain)
			fmt.Printf("transport rev: %s\n", info.TransportRev)
			fmt.Printf("protocols:     %s\n", strings.Join(info.Protocols, ", "))
			fmt.Printf("clients:       %d\n", info.Clients)
			fmt.Printf("cert expires:  %s\n", info.CertExpires)
			if info.WebProxyURL != "" {
				fmt.Printf("tg web proxy:  %s\n", info.WebProxyURL)
				fmt.Printf("bridge page:   %s\n", info.BridgeURL)
			}
			if info.MTProtoURL != "" {
				fmt.Printf("mtproto:       %s\n", info.MTProtoURL)
				fmt.Printf("mtproto fake:  %s\n", info.MTProtoTLSDomain)
			} else {
				fmt.Println("mtproto:       FakeTLS off (raw set-mtproto-tls --domain <third-party>)")
			}
			if info.SingBoxVersion != "" {
				fmt.Printf("singbox:       %s\n", info.SingBoxVersion)
			}
			fmt.Printf("doh:           %s\n", info.DohURL)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func setProtocolsCmd() *cobra.Command {
	var (
		protos []string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "set-protocols",
		Short: "Replace the node's server protocol set",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(protos) == 0 {
				return fmt.Errorf("--protocols is required")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			applied, err := nodeops.SetProtocols(cmd.Context(), store, opts, protos)
			if err != nil {
				return err
			}

			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"protocols": applied})
			}
			fmt.Printf("Protocols set to %s\n", strings.Join(applied, ", "))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&protos, "protocols", nil, "comma-separated protocols to enable")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func rotatePathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate-paths",
		Short: "Regenerate random transport paths; every issued client link dies",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			rev, err := nodeops.RotatePaths(cmd.Context(), store, opts)
			if err != nil {
				return err
			}

			fmt.Printf("Transport paths rotated, rev %s — every issued client link must be re-issued\n", rev)
			return nil
		},
	}
	return cmd
}

func setClientProtocolsCmd() *cobra.Command {
	var (
		name    string
		protos  []string
		inherit bool
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "set-client-protocols",
		Short: "Per-client protocol allowlist; --inherit goes back to the server set",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				return fmt.Errorf("client name is required")
			}
			if inherit == (len(protos) > 0) {
				return fmt.Errorf("either --protocols or --inherit is required, not both")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			var view *nodeops.ClientView
			if inherit {
				view, err = nodeops.InheritClientProtocols(cmd.Context(), store, opts, name)
			} else {
				view, err = nodeops.SetClientProtocols(cmd.Context(), store, opts, name, protos)
			}
			if err != nil {
				return err
			}

			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(view)
			}
			if inherit {
				fmt.Printf("Client %s inherits the server protocol set\n", name)
			} else {
				fmt.Printf("Client %s allowlist: %s\n", name, strings.Join(protos, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&protos, "protocols", nil, "comma-separated protocol allowlist")
	cmd.Flags().BoolVar(&inherit, "inherit", false, "reset to the server set")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func setMTProtoTLSCmd() *cobra.Command {
	var domain string
	cmd := &cobra.Command{
		Use:   "set-mtproto-tls",
		Short: "Enable MTProxy FakeTLS via a third-party domain; empty --domain disables",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			result, err := nodeops.SetMTProtoTLSDomain(cmd.Context(), store, opts, domain)
			if err != nil {
				return err
			}
			if result == "" {
				fmt.Println("MTProto FakeTLS disabled: the FakeTLS instance and its SNI branch are gone")
			} else {
				fmt.Printf("MTProto FakeTLS enabled: %s\nFakeTLS link is printed by `raw menu` (item 13)\n", result)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "third-party domain for FakeTLS (must differ from node domain and reality SNI)")
	return cmd
}

func setSNICmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "set-sni",
		Short: "Change the reality impersonation SNI; regenerates every client link",
		RunE: func(cmd *cobra.Command, args []string) error {
			if host == "" && len(args) > 0 {
				host = args[0]
			}
			if host == "" {
				return fmt.Errorf("reality SNI host is required")
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			rev, err := nodeops.SetRealitySNI(cmd.Context(), store, opts, host)
			if err != nil {
				return err
			}
			fmt.Printf("Reality SNI: %s — transport rev %s\nSubscriptions are regenerated; every issued client link must be re-issued\n", strings.ToLower(strings.TrimSpace(host)), rev)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "third-party SNI host (must differ from the mtproto tls domain)")
	return cmd
}

// versionCmd prints the compiled-in version, so a binary can be checked against its image tag in one line.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the binary version",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), config.Version)
		},
	}
}

func setCertCmd() *cobra.Command {
	var (
		certB64 string
		keyB64  string
	)
	cmd := &cobra.Command{
		Use:   "set-cert",
		Short: "Install custom/wildcard TLS certificate on the node",
		RunE: func(cmd *cobra.Command, args []string) error {
			if certB64 == "" || keyB64 == "" {
				return fmt.Errorf("--cert-base64 and --key-base64 are required")
			}
			certData, err := base64.StdEncoding.DecodeString(certB64)
			if err != nil {
				return fmt.Errorf("decode cert: %w", err)
			}
			keyData, err := base64.StdEncoding.DecodeString(keyB64)
			if err != nil {
				return fmt.Errorf("decode key: %w", err)
			}

			store, err := initStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			opts := setup.DefaultOptions(dataDir, installDir)
			domain, err := nodeops.SetCert(store, opts, certData, keyData)
			if err != nil {
				return err
			}
			fmt.Printf("Certificate installed for %s and nginx reloaded.\n", domain)
			return nil
		},
	}
	cmd.Flags().StringVar(&certB64, "cert-base64", "", "base64-encoded certificate PEM")
	cmd.Flags().StringVar(&keyB64, "key-base64", "", "base64-encoded private key PEM")
	return cmd
}
