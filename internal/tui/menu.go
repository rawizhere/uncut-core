package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/core"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/qrcode"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/supervisor"
	"github.com/rawizhere/uncut-core/internal/system"
	"github.com/rawizhere/uncut-core/internal/tproxy"
	"github.com/rawizhere/uncut-core/internal/updater"
)

type Menu struct {
	store      *db.Store
	sup        *supervisor.Supervisor
	installDir string
}

func NewMenu(store *db.Store, sup *supervisor.Supervisor, installDir string) *Menu {
	return &Menu{
		store:      store,
		sup:        sup,
		installDir: installDir,
	}
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
						huh.NewOption("7. Change Reality SNI", "sni"),
						huh.NewOption("8. Rotate Protocol Salts", "rotate_salts"),
						huh.NewOption("9. Service Logs", "logs"),
						huh.NewOption("10. Server Speedtest", "speedtest"),
						huh.NewOption("11. Telegram Web Proxy", "tg"),
						huh.NewOption("12. Maintenance", "maintenance"),
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
		case "sni":
			m.changeRealitySNI(ctx)
		case "rotate_salts":
			m.rotateSalts(ctx)
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
	if ssl != nil {
		fmt.Printf("SSL CA Issuer:   %s\n", ssl.Issuer)
		fmt.Printf("SSL Expiry Date: %s (%d days remaining)\n", ssl.NotAfter.Format("2006-01-02 15:04:05 UTC"), ssl.DaysRemaining)
		fmt.Printf("SSL Valid:       %t (Self-Signed: %t)\n", ssl.IsValid, ssl.SelfSigned)
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
		subURL := fmt.Sprintf("https://%s/assets/js/%s.bin", domain, c.SubHash)
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
	subURL := fmt.Sprintf("https://%s/assets/js/%s.bin", domain, client.SubHash)
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

	_ = m.store.DeleteClient(targetUUID)
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
	subURL := fmt.Sprintf("https://%s/assets/js/%s.bin", domain, client.SubHash)

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
			opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
			settings, _ := setup.Load(m.store, opts)
			links := core.GenerateClientLinks(*client, settings)
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
			label = "VLESS Reality (:8443 TCP)"
		case config.ProtoTUIC:
			label = "TUIC v5 (:443 UDP)"
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
	m.rebuildAndReload(ctx)
	fmt.Printf("\nProtocols updated successfully. Active: %s\n\n", strings.Join(selected, ", "))
}

func (m *Menu) changeRealitySNI(ctx context.Context) {
	currentSNI, _ := m.store.GetSetting("sni")
	if currentSNI == "" {
		currentSNI = config.DefaultSNI
	}

	var newSNI string
	input := huh.NewInput().
		Title(fmt.Sprintf("Current Reality SNI: %s\nEnter new SNI host:", currentSNI)).
		Value(&newSNI)

	if err := input.Run(); err != nil || strings.TrimSpace(newSNI) == "" {
		return
	}

	_ = m.store.SetSetting("sni", strings.TrimSpace(newSNI))
	m.rebuildAndReload(ctx)
	fmt.Printf("\nReality SNI updated to: %s\n\n", newSNI)
}

func (m *Menu) rotateSalts(ctx context.Context) {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Rotate protocol stealth paths and salts?").
				Value(&confirm),
		),
	)
	if err := form.Run(); err != nil || !confirm {
		return
	}

	newSalt := core.GeneratePassword()[:8]
	_ = m.store.SetSetting("protocol_salt", newSalt)
	m.rebuildAndReload(ctx)
	fmt.Printf("\nProtocol salt rotated: %s\n\n", newSalt)
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
	secret, _ := m.store.GetSetting("mtproto_secret")
	if secret == "" {
		secret = "2c4b671021c525b5563ee801f1f5da83"
	}

	cleanHex := strings.TrimPrefix(secret, "ee")
	if len(cleanHex) >= 32 {
		cleanHex = cleanHex[:32]
	}

	bridgeURL := tproxy.BridgeURL(domain, cleanHex)
	mtprotoLink := fmt.Sprintf("tg://proxy?server=%s&port=443&secret=%s", domain, secret)

	fmt.Println("\n=== Telegram Web Proxy ===")
	fmt.Printf("Host:        %s\n", domain)
	fmt.Printf("Port:        443\n")
	fmt.Printf("Secret:      %s\n", cleanHex)
	fmt.Printf("Bridge URL:  %s\n", bridgeURL)
	fmt.Printf("MTProto Link:%s\n", mtprotoLink)
	fmt.Println("==========================")
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
						huh.NewOption("3. Export Database Backup", "backup"),
						huh.NewOption("4. Import Database Backup", "import_backup"),
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
		case "backup":
			m.exportBackup()
		case "import_backup":
			m.importBackup(ctx)
		}
	}
}

func (m *Menu) switchSingboxVersion(ctx context.Context) {
	fmt.Println("\nFetching available releases from GitHub...")
	versions, err := updater.GetAvailableVersions(ctx, nil)
	if err != nil || len(versions) == 0 {
		fmt.Printf("Failed to fetch versions: %v\n\n", err)
		return
	}

	var opts []huh.Option[string]
	for _, v := range versions {
		opts = append(opts, huh.NewOption(v, v))
	}

	var targetVer string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select Sing-box Extended Version:").
				Options(opts...).
				Value(&targetVer),
		),
	)

	if err := form.Run(); err != nil || targetVer == "" {
		return
	}

	fmt.Printf("Downloading and installing %s...\n", targetVer)
	targetBinary := "/usr/local/bin/sing-box"
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
	opts := setup.DefaultOptions("/opt/uncut/data", m.installDir)
	if err := setup.RebuildAll(ctx, m.store, m.sup, opts); err != nil {
		fmt.Printf("Error rebuilding configs: %v\n", err)
	}
}
