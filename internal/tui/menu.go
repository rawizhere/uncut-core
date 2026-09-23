package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/links"
	"github.com/rawizhere/uncut-core/internal/nodeops"
	"github.com/rawizhere/uncut-core/internal/qrcode"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/system"
	"github.com/rawizhere/uncut-core/internal/transport"
	"github.com/rawizhere/uncut-core/internal/updater"
)

type Menu struct {
	store      *db.Store
	sup        *supervisor.Supervisor
	installDir string
	settings   *config.Settings
}

func NewMenu(store *db.Store, sup *supervisor.Supervisor, installDir string) *Menu {
	return &Menu{
		store:      store,
		sup:        sup,
		installDir: installDir,
	}
}

// loaded reads the settings once and keeps them; rebuildAndReload drops the cache so screens never show a stale copy after a rebuild.
func (m *Menu) loaded() *config.Settings {
	if m.settings != nil {
		return m.settings
	}
	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	s, err := setup.Load(m.store, opts)
	if err != nil {
		return nil
	}
	m.settings = &s
	return m.settings
}

func (m *Menu) Run(ctx context.Context) error {
	for {
		var action string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Uncut Core Management Console").
					Options(
						huh.NewOption("1. Server Status", "status"),
						huh.NewOption("2. List Clients", "clients"),
						huh.NewOption("3. Add Client", "add"),
						huh.NewOption("4. Delete Client", "del"),
						huh.NewOption("5. Client Details", "client_details"),
						huh.NewOption("6. Inbound Protocols", "protocols"),
						huh.NewOption("7. Change Domain", "change_domain"),
						huh.NewOption("8. Change Reality SNI", "sni"),
						huh.NewOption("9. MTProto FakeTLS", "mtproto_tls"),
						huh.NewOption("10. Rotate Transport Paths", "rotate_paths"),
						huh.NewOption("11. Service Logs", "logs"),
						huh.NewOption("12. Server Speedtest", "speedtest"),
						huh.NewOption("13. Telegram Web Proxy", "tg"),
						huh.NewOption("14. Maintenance", "maintenance"),
						huh.NewOption("0. Exit", "exit"),
					).
					Value(&action),
			),
		)

		if err := form.Run(); err != nil {
			return err
		}

		switch action {
		case "status":
			m.showStatus()
		case "clients":
			m.listClients()
		case "add":
			m.addClient(ctx)
		case "del":
			m.delClient(ctx)
		case "client_details":
			m.showClientDetails(ctx)
		case "protocols":
			m.manageProtocols(ctx)
		case "change_domain":
			m.changeDomain(ctx)
		case "sni":
			m.changeRealitySNI(ctx)
		case "mtproto_tls":
			m.changeMTProtoTLS(ctx)
		case "rotate_paths":
			m.rotatePaths(ctx)
		case "logs":
			m.showLogsMenu(ctx)
		case "speedtest":
			m.runSpeedtest(ctx)
		case "tg":
			m.showTGSecret()
		case "maintenance":
			m.showMaintenanceMenu(ctx)
		case "exit":
			return nil
		}
	}
}

func (m *Menu) showStatus() {
	domain, _ := m.store.GetSetting("domain")
	ip, _ := m.store.GetSetting("server_ip")
	clients, _ := m.store.GetClients()
	activeProtos, _ := m.store.GetSetting("protocols")

	host := system.GetHostMetrics("/opt/uncut/data")
	ssl, _ := system.GetSSLMetrics(m.installDir, domain)
	doh := ""
	if settings := m.loaded(); settings != nil && settings.Transport != nil {
		doh = "https://" + domain + settings.Transport.DoHPath()
	}

	fmt.Println("\n==================================")
	fmt.Printf("Server Hostname: %s\n", domain)
	fmt.Printf("Public IPv4:     %s\n", ip)
	fmt.Printf("Active Clients:  %d\n", len(clients))
	fmt.Printf("Active Inbounds: %s\n", activeProtos)
	fmt.Println("----------------------------------")
	fmt.Printf("CPU Utilization: %.1f%%\n", host.CPUUsagePercent)
	fmt.Printf("Memory Usage:    %d MB / %d MB (%.1f%%)\n", host.MemoryUsedMB, host.MemoryTotalMB, host.MemoryPercent)
	fmt.Printf("Disk Usage:      %.1f GB / %.1f GB (%.1f%%)\n", host.DiskUsedGB, host.DiskTotalGB, host.DiskPercent)
	fmt.Printf("System Uptime:   %s\n", host.UptimeFormatted)
	fmt.Println("----------------------------------")
	if v := updater.CurrentVersion(m.installDir); v != "" {
		fmt.Printf("Sing-box:        %s\n", v)
	}
	if doh != "" {
		fmt.Printf("DoH Endpoint:    %s\n", doh)
	}
	if ssl != nil {
		fmt.Printf("SSL CA Issuer:   %s\n", ssl.Issuer)
		fmt.Printf("SSL Expiry Date: %s (%d days remaining)\n", ssl.NotAfter.Format("2006-01-02 15:04:05 UTC"), ssl.DaysRemaining)
	} else {
		fmt.Println("SSL Status:      Pending / Not Found")
	}
	fmt.Println("==================================")
}

func (m *Menu) listClients() {
	domain, _ := m.store.GetSetting("domain")
	clients, err := m.store.GetClients()
	if err != nil || len(clients) == 0 {
		fmt.Println("\nNo active clients found.")
		return
	}

	fmt.Println("\nActive Clients:")
	for _, c := range clients {
		subURL := m.subscriptionURL(domain, c.SubHash)
		fmt.Printf("• %s (UUID: %s)\n  Subscription: %s\n", c.Name, c.UUID, subURL)
		asciiQR, err := qrcode.GenerateASCII(subURL)
		if err == nil {
			fmt.Println(asciiQR)
		}
	}
}

func (m *Menu) addClient(ctx context.Context) {
	var name string
	input := huh.NewInput().
		Title("Enter client name:").
		Value(&name)

	if err := input.Run(); err != nil || strings.TrimSpace(name) == "" {
		return
	}

	client, err := core.AddClientWithDefaultProtocols(m.store, name)
	if err != nil {
		fmt.Printf("\nFailed to add client: %v\n\n", err)
		return
	}

	m.rebuildAndReload(ctx)
	domain, _ := m.store.GetSetting("domain")
	subURL := m.subscriptionURL(domain, client.SubHash)
	fmt.Printf("\nClient %s created successfully.\nSubscription URL: %s\n", client.Name, subURL)

	qr, err := qrcode.GenerateASCII(subURL)
	if err == nil {
		fmt.Println(qr)
	}
}

func (m *Menu) delClient(ctx context.Context) {
	clients, _ := m.store.GetClients()
	if len(clients) == 0 {
		fmt.Println("\nNo clients to delete.")
		return
	}

	options := make([]huh.Option[string], 0, len(clients))
	for _, c := range clients {
		options = append(options, huh.NewOption(fmt.Sprintf("%s (%s)", c.Name, c.UUID), c.UUID))
	}

	var targetUUID string
	selectForm := huh.NewSelect[string]().
		Title("Select client to delete:").
		Options(options...).
		Value(&targetUUID)

	if err := selectForm.Run(); err != nil || targetUUID == "" {
		return
	}

	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	if err := core.DeleteClient(m.store, opts.SubsDir, targetUUID); err != nil {
		fmt.Printf("\nError deleting client: %v\n", err)
		return
	}

	m.rebuildAndReload(ctx)
	fmt.Println("\nClient deleted successfully.")
}

func (m *Menu) showClientDetails(ctx context.Context) {
	clients, _ := m.store.GetClients()
	if len(clients) == 0 {
		fmt.Println("\nNo clients available.")
		return
	}

	options := make([]huh.Option[string], 0, len(clients))
	for _, c := range clients {
		options = append(options, huh.NewOption(c.Name, c.UUID))
	}

	var selectedUUID string
	selForm := huh.NewSelect[string]().
		Title("Select Client:").
		Options(options...).
		Value(&selectedUUID)

	if err := selForm.Run(); err != nil || selectedUUID == "" {
		return
	}

	client, err := m.store.GetClientByUUID(selectedUUID)
	if err != nil {
		fmt.Printf("Client error: %v\n", err)
		return
	}

	domain, _ := m.store.GetSetting("domain")
	subURL := m.subscriptionURL(domain, client.SubHash)

	for {
		var action string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Client: %s", client.Name)).
					Options(
						huh.NewOption("1. View Subscription URL & QR", "sub"),
						huh.NewOption("2. View Raw Outbound Links", "links"),
						huh.NewOption("3. Edit Client Protocols", "protocols"),
						huh.NewOption("0. Back", "back"),
					).
					Value(&action),
			),
		)

		if err := form.Run(); err != nil || action == "back" || action == "" {
			return
		}

		switch action {
		case "sub":
			fmt.Printf("\nSubscription URL: %s\n", subURL)
			if qr, err := qrcode.GenerateASCII(subURL); err == nil {
				fmt.Println(qr)
			}
		case "links":
			s := m.loaded()
			if s == nil {
				fmt.Println("Failed to load settings.")
				break
			}
			links := core.GenerateClientLinks(*client, *s)
			fmt.Printf("\nRaw Links for %s:\n", client.Name)
			for _, l := range links {
				fmt.Println(l)
			}
		case "protocols":
			activeMap := make(map[string]bool)
			for _, p := range client.Protocols {
				activeMap[p] = true
			}
			var currentSelected []string
			for _, p := range config.ValidProtocols {
				if activeMap[string(p)] || len(client.Protocols) == 0 {
					currentSelected = append(currentSelected, string(p))
				}
			}

			var protoOpts []huh.Option[string]
			for _, p := range config.ValidProtocols {
				protoOpts = append(protoOpts, huh.NewOption(string(p), string(p)))
			}

			pForm := huh.NewForm(
				huh.NewGroup(
					huh.NewMultiSelect[string]().
						Title("Select allowed protocols for client:").
						Options(protoOpts...).
						Value(&currentSelected),
				),
			)
			if err := pForm.Run(); err == nil && len(currentSelected) > 0 {
				_, _ = core.UpdateClientProtocols(m.store, client.UUID, currentSelected)
				m.rebuildAndReload(ctx)
				fmt.Println("\nClient protocols updated.")
			}
		}
	}
}

func (m *Menu) manageProtocols(ctx context.Context) {
	activeStr, _ := m.store.GetSetting("protocols")
	activeList := strings.Split(activeStr, ",")
	activeMap := make(map[string]bool)
	for _, p := range activeList {
		p = strings.TrimSpace(p)
		if p != "" {
			activeMap[p] = true
		}
	}

	selected := make([]string, 0)
	for _, p := range config.ValidProtocols {
		if activeMap[string(p)] {
			selected = append(selected, string(p))
		}
	}

	var options []huh.Option[string]
	for _, p := range config.ValidProtocols {
		label := string(p)
		switch p {
		case config.ProtoVLESSReality:
			label = "VLESS Reality (:443 TCP via SNI split)"
		case config.ProtoHysteria2:
			label = "Hysteria2 (:443 UDP)"
		case config.ProtoXHTTPStealth:
			label = "VLESS XHTTP Stealth (:443 TCP via Nginx)"
		case config.ProtoVLESSWS:
			label = "VLESS WebSocket (:443 TCP via Nginx)"
		case config.ProtoVLESSHTTPUpgrade:
			label = "VLESS HTTPUpgrade (:443 TCP via Nginx)"
		case config.ProtoVLESSGRPC:
			label = "VLESS gRPC (:443 TCP via Nginx)"
		}
		options = append(options, huh.NewOption(label, string(p)))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select active inbound protocols:").
				Options(options...).
				Value(&selected),
		),
	)

	if err := form.Run(); err != nil {
		return
	}

	if len(selected) == 0 {
		fmt.Println("\nAt least one protocol must remain active.")
		return
	}

	_ = m.store.SetSetting("protocols", strings.Join(selected, ","))
	// Marked explicit so the next start does not merge the default set back in.
	_ = m.store.SetSetting("protocols_explicit", "true")
	m.rebuildAndReload(ctx)
	fmt.Printf("\nProtocols updated successfully. Active: %s\n\n", strings.Join(selected, ", "))
}

func (m *Menu) changeDomain(ctx context.Context) {
	currentDomain, _ := m.store.GetSetting("domain")

	var newDomain string
	input := huh.NewInput().
		Title(fmt.Sprintf("Current Domain: %s\nEnter new server domain:", currentDomain)).
		Value(&newDomain)

	if err := input.Run(); err != nil {
		return
	}

	newDomain = strings.TrimSpace(strings.ToLower(newDomain))
	if newDomain == "" || newDomain == currentDomain {
		return
	}

	var confirm bool
	cForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Confirm changing domain to %s?", newDomain)).
				Value(&confirm),
		),
	)
	if err := cForm.Run(); err != nil || !confirm {
		return
	}

	if err := m.store.SetSetting("domain", newDomain); err != nil {
		fmt.Printf("Failed to update domain: %v\n\n", err)
		return
	}

	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	appCfg, err := config.LoadAppConfig()
	if err == nil {
		acmeMgr := acme.New(newDomain, appCfg.Email, opts.CertsDir(), opts.WebRoot)
		fmt.Printf("\nRequesting TLS certificate for %s...\n", newDomain)
		if err := acmeMgr.ForceRenew(); err != nil {
			fmt.Printf("TLS certificate issuance warning: %v\n", err)
		}
	}

	m.rebuildAndReload(ctx)
	fmt.Printf("\nDomain successfully changed to %s and all subscriptions updated.\n\n", newDomain)
}

func (m *Menu) changeRealitySNI(ctx context.Context) {
	current := config.DefaultSNI
	if settings := m.loaded(); settings != nil {
		current = settings.RealityServerName
	}

	var newSNI string
	input := huh.NewInput().
		Title(fmt.Sprintf("Current Reality SNI: %s\nEnter new SNI host:", current)).
		Value(&newSNI)

	if err := input.Run(); err != nil || strings.TrimSpace(newSNI) == "" {
		return
	}

	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	rev, err := nodeops.SetRealitySNI(ctx, m.store, opts, newSNI)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("\nReality SNI updated: %s — transport rev %s, every client link must be re-issued\n\n", strings.TrimSpace(newSNI), rev)
}

func (m *Menu) rotatePaths(ctx context.Context) {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Rotate transport paths? Every issued client link dies.").
				Value(&confirm),
		),
	)
	if err := form.Run(); err != nil || !confirm {
		return
	}

	t, err := transport.Generate()
	if err != nil {
		fmt.Printf("Error generating transport: %v\n", err)
		return
	}
	if err := setup.SaveTransport(m.store, t); err != nil {
		fmt.Printf("Error saving transport: %v\n", err)
		return
	}
	m.rebuildAndReload(ctx)
	fmt.Printf("\nTransport paths rotated, rev %s — every issued client link must be re-issued\n\n", t.Rev())
}

func (m *Menu) runSpeedtest(ctx context.Context) {
	fmt.Println("\nRunning bandwidth benchmark...")
	res, err := system.RunSpeedtest(ctx)
	if err != nil {
		fmt.Printf("Speedtest failed: %v\n\n", err)
		return
	}

	fmt.Println("==================================")
	fmt.Printf("Server Latency: %.2f ms\n", res.LatencyMS)
	fmt.Printf("Download Speed: %.2f Mbps\n", res.DownloadMbps)
	fmt.Printf("Upload Speed:   %.2f Mbps\n", res.UploadMbps)
	fmt.Printf("Test Endpoint:  %s\n", res.TestEndpoint)
	fmt.Println("==================================")
}

func (m *Menu) showTGSecret() {
	domain, _ := m.store.GetSetting("domain")
	raw, _ := m.store.GetSetting("mtproto_raw_secret")
	tlsDomain, _ := m.store.GetSetting("mtproto_tls_domain")

	fmt.Println("\n=== Telegram Web Proxy ===")
	fmt.Printf("Host:       %s\n", domain)
	fmt.Printf("Port:       443\n")
	if raw != "" {
		fmt.Printf("Bridge URL: %s\n", links.BridgeURL(domain, raw))
	}
	// The FakeTLS link exists only when the branch is on; the old print was dead on arrival.
	if raw != "" && tlsDomain != "" {
		secret := links.MTProtoFakeTLSSecret(raw, tlsDomain)
		fmt.Printf("MTProto:    %s\n", links.MTProtoURL(domain, secret))
	} else {
		fmt.Println("MTProto:    FakeTLS off (item 9 or `raw set-mtproto-tls --domain <third-party>`)")
	}
	fmt.Println("==========================")
}

func (m *Menu) changeMTProtoTLS(ctx context.Context) {
	current, _ := m.store.GetSetting("mtproto_tls_domain")
	var input string
	form := huh.NewInput().
		Title(fmt.Sprintf("Current MTProto FakeTLS domain: %s\nThird-party domain (empty disables):", current)).
		Value(&input)
	if err := form.Run(); err != nil {
		return
	}
	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	result, err := nodeops.SetMTProtoTLSDomain(ctx, m.store, opts, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if result == "" {
		fmt.Println("\nMTProto FakeTLS disabled.")
	} else {
		fmt.Printf("\nMTProto FakeTLS enabled: %s (second instance on 2399)\n", result)
	}
}

func (m *Menu) showMaintenanceMenu(ctx context.Context) {
	for {
		var mAction string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Maintenance & Operations:").
					Options(
						huh.NewOption("1. Restart All Services", "restart"),
						huh.NewOption("2. Switch Sing-box Extended Version", "singbox_version"),
						huh.NewOption("3. Renew TLS Certificate", "renew_cert"),
						huh.NewOption("4. Export Database Backup", "backup"),
						huh.NewOption("5. Import Database Backup", "import_backup"),
						huh.NewOption("0. Back", "back"),
					).
					Value(&mAction),
			),
		)

		if err := form.Run(); err != nil || mAction == "back" || mAction == "" {
			return
		}

		switch mAction {
		case "restart":
			m.rebuildAndReload(ctx)
			fmt.Println("\nAll system services restarted successfully.")
		case "singbox_version":
			m.switchSingboxVersion(ctx)
		case "renew_cert":
			m.renewCertificate(ctx)
		case "backup":
			m.exportBackup()
		case "import_backup":
			m.importBackup(ctx)
		}
	}
}

func (m *Menu) renewCertificate(ctx context.Context) {
	settings := m.loaded()
	if settings == nil {
		fmt.Println("Failed to load settings.")
		return
	}

	fmt.Printf("\nRequesting TLS certificate for %s...\n", settings.Domain)
	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	acmeMgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot)
	if err := acmeMgr.ForceRenew(); err != nil {
		fmt.Printf("Certificate issuance error: %v\n\n", err)
		return
	}

	m.rebuildAndReload(ctx)
	fmt.Println("TLS Certificate renewed successfully and services reloaded.")
}

func (m *Menu) switchSingboxVersion(ctx context.Context) {
	fmt.Println("\nFetching available releases from GitHub...")
	var opts []huh.Option[string]
	versions, err := updater.GetAvailableVersions(ctx, nil)
	if err != nil || len(versions) == 0 {
		// api.github.com is unreliable from some datacenters; offer a manual version instead of dying on the fetch.
		fmt.Printf("Failed to fetch versions: %v\n", err)
		opts = append(opts, huh.NewOption("Enter a version manually", "?manual"))
	}

	current := updater.CurrentVersion(m.installDir)
	for _, v := range versions {
		if v != "" && v == current {
			opts = append(opts, huh.NewOption(v+" (current)", v))
		} else {
			opts = append(opts, huh.NewOption(v, v))
		}
	}

	var targetVer string
	groups := huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Select Sing-box Extended Version (current: %s):", updater.CurrentVersion(m.installDir))).
			Options(opts...).
			Value(&targetVer),
	)
	forms := []*huh.Group{groups}
	if err == nil || len(versions) > 0 {
		forms = append(forms, huh.NewGroup(
			huh.NewInput().
				Title("Sing-box Extended version (e.g. 1.13.18-extended-2.6.5):").
				Value(&targetVer),
		))
	}
	form := huh.NewForm(forms...)
	if err := form.Run(); err != nil || targetVer == "" || targetVer == "?manual" {
		return
	}

	fmt.Printf("Downloading and installing %s...\n", targetVer)
	targetBinary := filepath.Join(m.installDir, "bin", "sing-box")
	if err := updater.InstallSingboxVersion(ctx, targetVer, targetBinary); err != nil {
		fmt.Printf("Installation error: %v\n\n", err)
		return
	}

	m.rebuildAndReload(ctx)
	fmt.Printf("Sing-box updated to %s and service restarted.\n\n", targetVer)
}

func (m *Menu) exportBackup() {
	dbPath := "/opt/uncut/data/uncut.db"
	data, err := os.ReadFile(dbPath)
	if err != nil {
		fmt.Printf("Backup failed: %v\n\n", err)
		return
	}

	backupFile := fmt.Sprintf("/opt/uncut/data/backup_%s.db", time.Now().Format("20060102_150405"))
	if err := os.WriteFile(backupFile, data, 0o600); err != nil {
		fmt.Printf("Write backup failed: %v\n\n", err)
		return
	}

	fmt.Printf("\nBackup created successfully:\n%s\n\n", backupFile)
}

func (m *Menu) importBackup(ctx context.Context) {
	dataDir := "/opt/uncut/data"
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		fmt.Printf("Read data directory error: %v\n\n", err)
		return
	}

	var backupFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "backup_") && strings.HasSuffix(e.Name(), ".db") {
			backupFiles = append(backupFiles, filepath.Join(dataDir, e.Name()))
		}
	}

	if len(backupFiles) == 0 {
		fmt.Println("\nNo backup files found in /opt/uncut/data.")
		return
	}

	var opts []huh.Option[string]
	for _, f := range backupFiles {
		opts = append(opts, huh.NewOption(filepath.Base(f), f))
	}

	var selectedFile string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select backup database to restore:").
				Options(opts...).
				Value(&selectedFile),
		),
	)

	if err := form.Run(); err != nil || selectedFile == "" {
		return
	}

	var confirm bool
	cForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Overwrite database with %s?", filepath.Base(selectedFile))).
				Value(&confirm),
		),
	)
	if err := cForm.Run(); err != nil || !confirm {
		return
	}

	backupData, err := os.ReadFile(selectedFile)
	if err != nil {
		fmt.Printf("Failed to read backup: %v\n\n", err)
		return
	}

	targetDB := filepath.Join(dataDir, "uncut.db")
	if err := os.WriteFile(targetDB, backupData, 0o600); err != nil {
		fmt.Printf("Failed to restore database: %v\n\n", err)
		return
	}

	m.rebuildAndReload(ctx)
	fmt.Printf("\nDatabase restored from %s and configurations reloaded.\n\n", filepath.Base(selectedFile))
}

func (m *Menu) rebuildAndReload(ctx context.Context) {
	m.settings = nil
	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	if err := setup.RebuildAll(ctx, m.store, m.sup, opts); err != nil {
		fmt.Printf("Error rebuilding configs: %v\n", err)
	}
}

// subscriptionURL renders the subscription URL with the node's stored transport.
func (m *Menu) subscriptionURL(domain, subHash string) string {
	if settings := m.loaded(); settings != nil && settings.Transport != nil {
		return settings.Transport.SubURL(domain, subHash)
	}
	// No settings, no URL: a built-in default would be a link that dies the moment the real transport loads.
	return ""
}
