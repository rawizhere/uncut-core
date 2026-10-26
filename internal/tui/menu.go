package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/nodeops"
	"github.com/rawizhere/uncut-core/internal/qrcode"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/system"
	"github.com/rawizhere/uncut-core/internal/updater"
)

// Menu is the human front-end; every change goes through nodeops, same as the CLI.
type Menu struct {
	store *db.Store
	opts  setup.Options
}

func NewMenu(store *db.Store, opts setup.Options) *Menu {
	return &Menu{store: store, opts: opts}
}

// loaded reads the settings fresh each time, so a screen never shows a copy from before a nodeops change.
func (m *Menu) loaded() *config.Settings {
	s, err := setup.Load(m.store, m.opts)
	if err != nil {
		return nil
	}
	return &s
}

// current is loaded for screens that only display a value: a load failure shows blanks.
func (m *Menu) current() config.Settings {
	if s := m.loaded(); s != nil {
		return *s
	}
	return config.Settings{}
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
			m.listClients(ctx)
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

// showStatus is the human view of the same nodeops.GetInfo that `raw info` prints, plus host metrics.
func (m *Menu) showStatus() {
	info, err := nodeops.GetInfo(m.store, m.opts)
	if err != nil {
		fmt.Printf("\nFailed to load node state: %v\n", err)
		return
	}
	host := system.GetHostMetrics(m.opts.DataDir)

	fmt.Println("\n==================================")
	fmt.Printf("Server Hostname: %s\n", info.Domain)
	fmt.Printf("Public IPv4:     %s\n", m.current().IP)
	fmt.Printf("Active Clients:  %d\n", info.Clients)
	fmt.Printf("Active Inbounds: %s\n", strings.Join(info.Protocols, ", "))
	fmt.Println("----------------------------------")
	fmt.Printf("CPU Utilization: %.1f%%\n", host.CPUUsagePercent)
	fmt.Printf("Memory Usage:    %d MB / %d MB (%.1f%%)\n", host.MemoryUsedMB, host.MemoryTotalMB, host.MemoryPercent)
	fmt.Printf("Disk Usage:      %.1f GB / %.1f GB (%.1f%%)\n", host.DiskUsedGB, host.DiskTotalGB, host.DiskPercent)
	fmt.Printf("System Uptime:   %s\n", host.UptimeFormatted)
	fmt.Println("----------------------------------")
	if info.SingBoxVersion != "" {
		fmt.Printf("Sing-box:        %s\n", info.SingBoxVersion)
	}
	fmt.Printf("DoH Endpoint:    %s\n", info.DohURL)
	if info.CertExpires != "" {
		fmt.Printf("SSL Expiry Date: %s\n", info.CertExpires)
	} else {
		fmt.Println("SSL Status:      Pending / Not Found")
	}
	fmt.Println("==================================")
}

func (m *Menu) listClients(ctx context.Context) {
	list, err := nodeops.ListClients(ctx, m.store, m.opts)
	if err != nil || len(list.Clients) == 0 {
		fmt.Println("\nNo active clients found.")
		return
	}

	fmt.Println("\nActive Clients:")
	for _, c := range list.Clients {
		fmt.Printf("• %s (UUID: %s)\n  Subscription: %s\n", c.Name, c.UUID, c.Subscription)
		printQR(c.Subscription)
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

	view, err := nodeops.AddClient(ctx, m.store, m.opts, name, nil)
	if err != nil {
		fmt.Printf("\nFailed to add client: %v\n\n", err)
		return
	}

	fmt.Printf("\nClient %s created successfully.\nSubscription URL: %s\n", view.Name, view.Subscription)
	printQR(view.Subscription)
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

	if err := nodeops.DeleteClient(ctx, m.store, m.opts, targetUUID); err != nil {
		fmt.Printf("\nError deleting client: %v\n", err)
		return
	}
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

	for {
		// Re-read every round: a protocol change below rewrites the client.
		client, err := m.store.GetClientByUUID(selectedUUID)
		if err != nil {
			fmt.Printf("Client error: %v\n", err)
			return
		}

		var action string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Client: %s", client.Name)).
					Options(
						huh.NewOption("1. View Subscription URL & QR", "sub"),
						huh.NewOption("2. View Raw Outbound Links", "links"),
						huh.NewOption("3. Edit Client Protocols", "protocols"),
						huh.NewOption("4. Inherit Server Protocols", "inherit"),
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
			subURL := m.subscriptionURL(client.SubHash)
			fmt.Printf("\nSubscription URL: %s\n", subURL)
			printQR(subURL)
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
			for _, p := range config.DefaultProtocols {
				if activeMap[string(p)] || len(client.Protocols) == 0 {
					currentSelected = append(currentSelected, string(p))
				}
			}

			var protoOpts []huh.Option[string]
			for _, p := range config.DefaultProtocols {
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
				if _, err := nodeops.SetClientProtocols(ctx, m.store, m.opts, client.Name, currentSelected); err != nil {
					fmt.Printf("Error: %v\n", err)
					break
				}
				fmt.Println("\nClient protocols updated.")
			}
		case "inherit":
			if _, err := nodeops.InheritClientProtocols(ctx, m.store, m.opts, client.Name); err != nil {
				fmt.Printf("Error: %v\n", err)
				break
			}
			fmt.Println("\nClient now inherits the server protocol set.")
		}
	}
}

func (m *Menu) manageProtocols(ctx context.Context) {
	activeMap := make(map[string]bool)
	for _, p := range m.current().Protocols {
		activeMap[p] = true
	}

	selected := make([]string, 0)
	for _, p := range config.DefaultProtocols {
		if activeMap[string(p)] {
			selected = append(selected, string(p))
		}
	}

	var options []huh.Option[string]
	for _, p := range config.DefaultProtocols {
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

	applied, err := nodeops.SetProtocols(ctx, m.store, m.opts, selected)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("\nProtocols updated successfully. Active: %s\n\n", strings.Join(applied, ", "))
}

func (m *Menu) changeDomain(ctx context.Context) {
	currentDomain := m.current().Domain

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

	fmt.Printf("\nRequesting TLS certificate for %s...\n", newDomain)
	if err := nodeops.ChangeDomain(ctx, m.store, m.opts, newDomain); err != nil {
		fmt.Printf("Failed to update domain: %v\n\n", err)
		return
	}
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

	rev, err := nodeops.SetRealitySNI(ctx, m.store, m.opts, newSNI)
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

	rev, err := nodeops.RotatePaths(ctx, m.store, m.opts)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("\nTransport paths rotated, rev %s — every issued client link must be re-issued\n\n", rev)
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
	info, err := nodeops.GetInfo(m.store, m.opts)
	if err != nil {
		fmt.Printf("\nFailed to load node state: %v\n", err)
		return
	}

	fmt.Println("\n=== Telegram Web Proxy ===")
	fmt.Printf("Host:       %s\n", info.Domain)
	fmt.Printf("Port:       443\n")
	if info.WebProxyURL != "" {
		fmt.Printf("Web proxy:  %s\n", info.WebProxyURL)
		fmt.Printf("Bridge URL: %s\n", info.BridgeURL)
	}
	if info.MTProtoURL != "" {
		fmt.Printf("MTProto:    %s\n", info.MTProtoURL)
		printQR(info.MTProtoURL)
	} else {
		fmt.Println("MTProto:    FakeTLS off (item 9 or `raw set-mtproto-tls --domain <third-party>`)")
	}
	fmt.Println("==========================")
}

func (m *Menu) changeMTProtoTLS(ctx context.Context) {
	current := m.current().MTProtoTLSDomain
	var input string
	form := huh.NewInput().
		Title(fmt.Sprintf("Current MTProto FakeTLS domain: %s\nThird-party domain (empty disables):", current)).
		Value(&input)
	if err := form.Run(); err != nil {
		return
	}
	result, err := nodeops.SetMTProtoTLSDomain(ctx, m.store, m.opts, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if result == "" {
		fmt.Println("\nMTProto FakeTLS disabled.")
	} else {
		fmt.Printf("\nMTProto FakeTLS enabled: %s (sing-box mtproxy inbound)\n", result)
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
						huh.NewOption("1. Rebuild & Reload Configs", "restart"),
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
			if err := setup.Reload(ctx); err != nil {
				fmt.Printf("Error: %v\n", err)
				break
			}
			fmt.Println("\nConfigurations rebuilt and services reloaded.")
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
	if err := nodeops.RenewCert(ctx, m.store, m.opts); err != nil {
		fmt.Printf("Certificate issuance error: %v\n\n", err)
		return
	}
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

	current := updater.CurrentVersion(m.opts.InstallDir)
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
			Title(fmt.Sprintf("Select Sing-box Extended Version (current: %s):", current)).
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
	if err := nodeops.SwitchSingbox(ctx, m.opts, targetVer); err != nil {
		fmt.Printf("Installation error: %v\n\n", err)
		return
	}
	fmt.Printf("Sing-box updated to %s and service restarted.\n\n", targetVer)
}

func (m *Menu) exportBackup() {
	backupFile, err := nodeops.ExportBackup(m.store, m.opts)
	if err != nil {
		fmt.Printf("Backup failed: %v\n\n", err)
		return
	}

	fmt.Printf("\nBackup created successfully:\n%s\n\n", backupFile)
}

func (m *Menu) importBackup(ctx context.Context) {
	backupFiles, err := nodeops.Backups(m.opts)
	if err != nil {
		fmt.Printf("Read data directory error: %v\n\n", err)
		return
	}
	if len(backupFiles) == 0 {
		fmt.Printf("\nNo backup files found in %s.\n", m.opts.DataDir)
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

	if err := nodeops.ImportBackup(ctx, m.store, m.opts, selectedFile); err != nil {
		fmt.Printf("Failed to restore database: %v\n\n", err)
		return
	}
	fmt.Printf("\nDatabase restored from %s and configurations reloaded.\n\n", filepath.Base(selectedFile))
}

// printQR prints the URL as an ASCII QR code; an unencodable URL just prints nothing.
func printQR(url string) {
	if qr, err := qrcode.GenerateASCII(url); err == nil {
		fmt.Println(qr)
	}
}

// subscriptionURL renders the subscription URL with the node's stored transport.
func (m *Menu) subscriptionURL(subHash string) string {
	if settings := m.loaded(); settings != nil && settings.Transport != nil {
		return settings.Transport.SubURL(settings.Domain, subHash)
	}
	// No settings, no URL: a built-in default would be a link that dies the moment the real transport loads.
	return ""
}
