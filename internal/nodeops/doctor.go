package nodeops

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/acme"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
	"github.com/rawizhere/uncut-core/internal/setup"
	"github.com/rawizhere/uncut-core/internal/tproxy"
)

// Check is one doctor probe result; Fix is empty when Pass.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Doctor runs every check that is possible from inside the container. Host-level facts (ufw, fail2ban) are out of scope here: they are runbook steps.
func Doctor(ctx context.Context, store *db.Store, opts setup.Options) []Check {
	settings, err := setup.Load(store, opts)
	if err != nil {
		return []Check{{Name: "settings", Pass: false, Detail: err.Error()}}
	}

	checks := []Check{checkDaemon(ctx), checkConfigs(ctx, opts)}
	checks = append(checks, checkDNS(ctx, settings)...)
	checks = append(checks, checkPorts()...)
	checks = append(checks, checkCertificate(settings, opts))
	checks = append(checks, checkTransports(settings)...)
	checks = append(checks, checkBridge(ctx, settings))
	checks = append(checks, checkSubscriptions(store, opts))
	checks = append(checks, checkKernelNetwork()...)
	checks = append(checks, checkHysteriaBandwidth(settings))
	checks = append(checks, checkSwap())

	return checks
}

func checkDaemon(ctx context.Context) Check {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+setup.DaemonAddr+"/healthz", nil)
	if err != nil {
		return Check{Name: "daemon", Detail: err.Error()}
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return Check{Name: "daemon", Detail: err.Error(), Fix: "restart the container"}
	}
	defer func() { _ = resp.Body.Close() }()
	var health struct {
		Processes map[string]bool `json:"processes"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&health)
	var down []string
	for name, ok := range health.Processes {
		if !ok {
			down = append(down, name)
		}
	}
	if len(down) > 0 || resp.StatusCode != http.StatusOK {
		slices.Sort(down)
		return Check{Name: "daemon", Detail: "down: " + strings.Join(down, ", "), Fix: "check the process logs in /opt/uncut/data/logs"}
	}
	return Check{Name: "daemon", Pass: true, Detail: fmt.Sprintf("%d processes up", len(health.Processes))}
}

func checkConfigs(ctx context.Context, opts setup.Options) Check {
	if err := setup.CheckConfigs(ctx, opts); err != nil {
		return Check{Name: "configs", Detail: err.Error(), Fix: "raw menu: rebuild, then read the daemon logs"}
	}
	return Check{Name: "configs", Pass: true, Detail: "sing-box check and nginx -t pass"}
}

func checkDNS(ctx context.Context, settings config.Settings) []Check {
	domain := settings.Domain
	checks := []Check{}
	// The node runs IPv4 only: any AAAA sends v6-preferring clients somewhere that is not this node.
	if v6, err := net.DefaultResolver.LookupIP(ctx, "ip6", domain); err == nil && len(v6) > 0 {
		checks = append(checks, Check{Name: "dns aaaa", Detail: domain + " has AAAA " + v6[0].String(), Fix: "remove the AAAA record: the node serves IPv4 only"})
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", domain)
	if err != nil {
		return append(checks, Check{Name: "dns", Detail: domain + ": " + err.Error(), Fix: "check the A record and this host's resolver"})
	}
	resolved := ""
	if len(ips) > 0 {
		resolved = ips[0].String()
	}
	if settings.IP != "" && resolved != "" && settings.IP != resolved {
		return append(checks, Check{Name: "dns", Detail: fmt.Sprintf("%s resolves to %s, pinned IP is %s", domain, resolved, settings.IP),
			Fix: "update the A record or re-run: raw sync-ip"})
	}
	detail := domain + " -> " + resolved
	if settings.IP == "" {
		detail += " (no pinned IP to compare)"
	}
	return append(checks, Check{Name: "dns", Pass: true, Detail: detail})
}

func checkPorts() []Check {
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
	return checks
}

func checkCertificate(settings config.Settings, opts setup.Options) Check {
	cert, err := acme.New(settings.Domain, settings.Email, opts.CertsDir(), opts.WebRoot).Certificate()
	if err != nil {
		return Check{Name: "certificate", Pass: false, Detail: "not issued", Fix: "check the DNS A record and that port 80 is reachable; see raw renew-cert"}
	}
	if err := cert.VerifyHostname(settings.Domain); err != nil {
		return Check{Name: "certificate", Detail: err.Error(), Fix: "raw renew-cert"}
	}
	expires := cert.NotAfter
	days := int(time.Until(expires).Hours() / 24)
	if days < 14 {
		return Check{Name: "certificate", Pass: false, Detail: fmt.Sprintf("expires %s, %d days left", expires.Format(time.DateOnly), days), Fix: "raw renew-cert"}
	}
	return Check{Name: "certificate", Pass: true, Detail: fmt.Sprintf("expires %s, %d days left", expires.Format(time.DateOnly), days)}
}

func checkTransports(settings config.Settings) []Check {
	var checks []Check
	for _, proto := range settings.Protocols {
		port, ok := config.ProtocolPort(proto)
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
	if slices.Contains(settings.Protocols, string(config.ProtoHysteria2)) && !udpListens(config.Hysteria2Port) {
		checks = append(checks, Check{Name: "transport hysteria2", Pass: false, Detail: "udp " + config.Hysteria2Port + " not listening", Fix: "check sing-box logs"})
	}
	return checks
}

func checkBridge(ctx context.Context, settings config.Settings) Check {
	// CheckBridge retries for a starting daemon; a doctor run waits 10s at most.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tproxy.CheckBridge(ctx, settings.Domain, settings.WebProxySecret); err != nil {
		return Check{Name: "tg bridge", Detail: err.Error(), Fix: "check tproxy-server.log"}
	}
	if err := tproxy.CheckBackend(ctx, ""); err != nil {
		return Check{Name: "tg bridge", Detail: err.Error(), Fix: "check mtproto-proxy.log"}
	}
	return Check{Name: "tg bridge", Pass: true, Detail: "page and backend answer"}
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
	if files != len(clients) {
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

// checkKernelNetwork reads the socket-buffer and congestion sysctls host.sh sets; a skipped read (no /proc) is not a failure.
func checkKernelNetwork() []Check {
	var checks []Check

	for _, buf := range []struct {
		name, path string
	}{{"kernel rmem_max", "/proc/sys/net/core/rmem_max"}, {"kernel wmem_max", "/proc/sys/net/core/wmem_max"}} {
		data, err := os.ReadFile(buf.path)
		if err != nil {
			continue
		}
		val, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		mb := val / 1048576
		if val < 8388608 {
			checks = append(checks, Check{
				Name:   buf.name,
				Pass:   false,
				Detail: fmt.Sprintf("%d bytes (%d MB); below 8 MB the UDP socket buffer drops packets under load", val, mb),
				Fix:    "run deployments/host.sh on host or set sysctl net.core." + strings.TrimPrefix(buf.name, "kernel ") + "=16777216",
			})
			continue
		}
		checks = append(checks, Check{
			Name:   buf.name,
			Pass:   true,
			Detail: fmt.Sprintf("%d bytes (%d MB)", val, mb),
		})
	}

	ccData, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err == nil {
		cc := strings.TrimSpace(string(ccData))
		if cc != "bbr" {
			checks = append(checks, Check{
				Name:   "tcp congestion",
				Pass:   false,
				Detail: cc + " (bbr recommended for VPN throughput)",
				Fix:    "run deployments/host.sh on host or set sysctl net.ipv4.tcp_congestion_control=bbr",
			})
		} else {
			checks = append(checks, Check{
				Name:   "tcp congestion",
				Pass:   true,
				Detail: "bbr",
			})
		}
	}

	return checks
}

func checkHysteriaBandwidth(settings config.Settings) Check {
	if !slices.Contains(settings.Protocols, string(config.ProtoHysteria2)) {
		return Check{Name: "hysteria2 bandwidth", Pass: true, Detail: "hysteria2 disabled"}
	}
	if settings.Hysteria2UpMbps != "" && settings.Hysteria2DownMbps != "" {
		return Check{
			Name:   "hysteria2 bandwidth",
			Pass:   true,
			Detail: fmt.Sprintf("brutal caps: up %s Mbps, down %s Mbps", settings.Hysteria2UpMbps, settings.Hysteria2DownMbps),
		}
	}
	return Check{
		Name:   "hysteria2 bandwidth",
		Pass:   true,
		Detail: "uncapped (BBR fallback)",
	}
}

func checkSwap() Check {
	data, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return Check{Name: "swap", Pass: false, Detail: err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= 1 {
		return Check{
			Name:   "swap",
			Pass:   false,
			Detail: "no swap configured",
			Fix:    "add a 1-2GB swap file on host to prevent OOM killer on low-RAM VPS",
		}
	}
	return Check{Name: "swap", Pass: true, Detail: fmt.Sprintf("%d active swap partition(s)/file(s)", len(lines)-1)}
}

