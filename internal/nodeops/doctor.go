package nodeops

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/setup"
)

// Check is one doctor probe result; Fix is empty when Pass.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// transportPorts maps enabled protocols to their loopback listeners.
var transportPorts = map[string]int{
	string(config.ProtoVLESSWS):          10001,
	string(config.ProtoXHTTPStealth):     10002,
	string(config.ProtoVLESSGRPC):        10003,
	string(config.ProtoVLESSHTTPUpgrade): 10004,
	string(config.ProtoVLESSReality):     8443,
}

// Doctor runs every check that is possible from inside the container.
// Host-level facts (ufw, fail2ban) are out of scope here: they are runbook steps.
func Doctor(ctx context.Context, store *db.Store, opts setup.Options) []Check {
	settings, err := setup.Load(store, opts)
	if err != nil {
		return []Check{{Name: "settings", Pass: false, Detail: err.Error()}}
	}

	checks := []Check{checkDNS(ctx, store, settings.Domain)}
	checks = append(checks, checkPorts(settings)...)
	checks = append(checks, checkCertificate(settings, opts))
	checks = append(checks, checkTransports(ctx, settings)...)
	checks = append(checks, checkSubscriptions(store, opts))

	return checks
}

func checkDNS(ctx context.Context, store *db.Store, domain string) Check {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", domain)
	if err != nil {
		return Check{Name: "dns", Detail: domain + ": " + err.Error(), Fix: "check the A record and this host's resolver"}
	}
	resolved := ""
	if len(ips) > 0 {
		resolved = ips[0].String()
	}
	if pinned, _ := store.GetSetting("server_ip"); pinned != "" && resolved != "" && pinned != resolved {
		return Check{Name: "dns", Pass: false, Detail: fmt.Sprintf("%s resolves to %s, pinned IP is %s", domain, resolved, pinned),
			Fix: "update the A record or re-run: raw sync-ip"}
	}
	detail := domain + " -> " + resolved
	if pinned, _ := store.GetSetting("server_ip"); pinned == "" {
		detail += " (no pinned IP to compare)"
	}
	return Check{Name: "dns", Pass: true, Detail: detail}
}

func checkPorts(settings config.Settings) []Check {
	var checks []Check
	for _, p := range []struct {
		name string
		port string
		udp  bool
	}{{"port 80/tcp", "80", false}, {"port 443/tcp", "443", false}, {"port 443/udp", "443", true}} {
		if p.udp {
			if udpListens(p.port) {
				checks = append(checks, Check{Name: p.name, Pass: true, Detail: "listening"})
			} else {
				checks = append(checks, Check{Name: p.name, Pass: false, Detail: "not listening", Fix: "check sing-box and the container restart"})
			}
			continue
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+p.port, 2*time.Second)
		if err != nil {
			checks = append(checks, Check{Name: p.name, Pass: false, Detail: err.Error(), Fix: "check nginx inside the container"})
			continue
		}
		_ = conn.Close()
		checks = append(checks, Check{Name: p.name, Pass: true, Detail: "accepting"})
	}
	if tuicPort(settings) != "" && !udpListens(tuicPort(settings)) {
		checks = append(checks, Check{Name: "tuic udp", Pass: false, Detail: "port " + tuicPort(settings) + " not listening", Fix: "check sing-box logs"})
	}
	return checks
}

func tuicPort(settings config.Settings) string {
	if settings.TUICPort != "" {
		return settings.TUICPort
	}
	return config.DefaultTUICPort
}

func checkCertificate(settings config.Settings, opts setup.Options) Check {
	mgr := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot)
	expires, err := mgr.Expires()
	if err != nil {
		return Check{Name: "certificate", Pass: false, Detail: "not issued", Fix: "check the DNS A record and that port 80 is reachable; see raw renew-cert"}
	}
	days := int(time.Until(expires).Hours() / 24)
	if days < 14 {
		return Check{Name: "certificate", Pass: false, Detail: fmt.Sprintf("expires %s, %d days left", expires.Format(time.DateOnly), days), Fix: "raw renew-cert"}
	}
	return Check{Name: "certificate", Pass: true, Detail: fmt.Sprintf("expires %s, %d days left", expires.Format(time.DateOnly), days)}
}

func checkTransports(ctx context.Context, settings config.Settings) []Check {
	var checks []Check
	for _, proto := range settings.Protocols {
		port, ok := transportPorts[proto]
		if !ok {
			continue
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
		if err != nil {
			checks = append(checks, Check{Name: "transport " + proto, Pass: false, Detail: err.Error(), Fix: "check sing-box logs in /opt/uncut/data/logs"})
			continue
		}
		_ = conn.Close()
		checks = append(checks, Check{Name: "transport " + proto, Pass: true, Detail: "accepting on 127.0.0.1:" + strconv.Itoa(port)})
	}
	if port := tuicPort(settings); slicesContains(settings.Protocols, string(config.ProtoTUIC)) && !udpListens(port) {
		checks = append(checks, Check{Name: "transport tuic", Pass: false, Detail: "udp " + port + " not listening", Fix: "check sing-box logs"})
	}
	return checks
}

func checkSubscriptions(store *db.Store, opts setup.Options) Check {
	clients, err := store.GetClients()
	if err != nil {
		return Check{Name: "subscriptions", Pass: false, Detail: err.Error()}
	}
	entries, err := os.ReadDir(opts.SubsDir)
	if err != nil {
		return Check{Name: "subscriptions", Pass: false, Detail: opts.SubsDir + ": " + err.Error(), Fix: "raw menu: rebuild, or restart the container"}
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			files++
		}
	}
	if files < len(clients) {
		return Check{Name: "subscriptions", Pass: false, Detail: fmt.Sprintf("%d files for %d clients", files, len(clients)), Fix: "restart the container to trigger a rebuild"}
	}
	return Check{Name: "subscriptions", Pass: true, Detail: fmt.Sprintf("%d files for %d clients", files, len(clients))}
}

func udpListens(port string) bool {
	num, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	hexPort := fmt.Sprintf("%04X", num)
	data, err := os.ReadFile("/proc/net/udp")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if addr := strings.Split(fields[1], ":"); len(addr) == 2 && addr[1] == hexPort {
			return true
		}
	}
	return false
}

func slicesContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
