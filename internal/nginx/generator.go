package nginx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/renameio/v2"
	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/transport"
)

const (
	subsZoneName = "anti_subs"
	subsRate     = "10r/s"

	// StreamConfDir sits at nginx MAIN context; conf.d is http-only.
	StreamConfDir  = "/etc/nginx/stream-conf.d"
	StreamFileName = "uncut-stream.conf"

	// RealityInboundPort and NginxHTTPSBackendPort: the loopback pair behind the SNI split.
	RealityInboundPort    = 8443
	NginxHTTPSBackendPort = 8442
	// MTProtoTLSInboundPort: loopback FakeTLS instance; 2398 stays plain for tproxy.
	MTProtoTLSInboundPort = 2399
)

type GeneratorOptions struct {
	Domain            string
	InstallDir        string
	SubsDir           string
	LogDir            string
	WebRoot           string
	ProtocolSalt      string
	ActiveProtocols   []string
	TelegramProxyPort int
	Transport         *transport.Transport
	// RealityServerName: the SNI routed to the reality inbound, not https.
	RealityServerName string
	MTProtoTLSDomain  string
}

type Paths struct {
	IncludeDir string
	ServerFile string
	Locations  string
	LimitsFile string
}

func (opts *GeneratorOptions) normalize() {
	if opts.InstallDir == "" {
		opts.InstallDir = "/opt/sing-box"
	}
	if opts.SubsDir == "" {
		opts.SubsDir = "/var/www/cdn/subs"
	}
	if opts.LogDir == "" {
		opts.LogDir = "/var/log/nginx"
	}
	if opts.WebRoot == "" {
		opts.WebRoot = "/var/www/html"
	}
	if opts.TelegramProxyPort <= 0 {
		opts.TelegramProxyPort = config.TelegramProxyPort()
	}
	if opts.RealityServerName == "" {
		opts.RealityServerName = config.DefaultSNI
	}
}

func DetectPaths(installDir string) Paths {
	paths := Paths{
		Locations: filepath.Join(installDir, "nginx_locations.conf"),
	}

	switch {
	case dirExists("/etc/nginx/conf.d"):
		// conf.d ships with both Debian and nginx.org mainline; http.d does not.
		paths.IncludeDir = "/etc/nginx/conf.d"
		paths.ServerFile = "/etc/nginx/conf.d/uncut.conf"
	case dirExists("/etc/nginx/sites-enabled"):
		paths.IncludeDir = "/etc/nginx/sites-enabled"
		paths.ServerFile = "/etc/nginx/sites-available/uncut"
	default:
		paths.IncludeDir = "/etc/nginx/http.d"
		paths.ServerFile = "/etc/nginx/http.d/uncut.conf"
	}

	paths.LimitsFile = filepath.Join(paths.IncludeDir, "00-uncut-limits.conf")
	return paths
}

func WriteFiles(opts GeneratorOptions, paths Paths) error {
	opts.normalize()

	for _, dir := range []string{paths.IncludeDir, opts.SubsDir, opts.LogDir, opts.WebRoot, StreamConfDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := write(paths.Locations, []byte(GenerateLocationsConfig(opts)), 0o644); err != nil {
		return err
	}
	if err := write(paths.ServerFile, []byte(GenerateServerConfig(opts)), 0o644); err != nil {
		return err
	}
	// The stream splitter lives at main context, outside the server conf.
	streamBody, err := GenerateStreamConfig(opts)
	if err != nil {
		return err
	}
	if err := write(filepath.Join(StreamConfDir, StreamFileName), []byte(streamBody), 0o644); err != nil {
		return err
	}

	if err := writeLimits(paths.LimitsFile); err != nil {
		return err
	}

	if filepath.Dir(paths.ServerFile) != paths.IncludeDir {
		return linkServerFile(paths)
	}
	return nil
}

func writeLimits(path string) error {
	body := fmt.Sprintf("limit_req_zone $binary_remote_addr zone=%s:10m rate=%s;\n", subsZoneName, subsRate)

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), "zone="+subsZoneName) {
			return nil
		}
	}
	return write(path, []byte(body), 0o644)
}

func linkServerFile(paths Paths) error {
	link := filepath.Join(paths.IncludeDir, "uncut")
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	if err := os.Symlink(paths.ServerFile, link); err != nil {
		return fmt.Errorf("enable server block: %w", err)
	}
	return nil
}

func GenerateLocationsConfig(opts GeneratorOptions) string {
	opts.normalize()
	active := make(map[string]bool, len(opts.ActiveProtocols))
	for _, p := range config.ResolveProtocols(opts.ActiveProtocols) {
		active[strings.TrimSpace(p)] = true
	}
	var sb strings.Builder

	if active[string(config.ProtoXHTTPStealth)] {
		xhttpPath := opts.Transport.XHTTPPath()
		fmt.Fprintf(&sb, "    location ^~ %s {\n", xhttpPath)
		sb.WriteString("        proxy_pass http://xhttp_backend;\n")
		sb.WriteString("        proxy_http_version 1.1;\n")
		sb.WriteString("        proxy_set_header Connection \"\";\n")
		sb.WriteString("        proxy_buffering off;\n")
		sb.WriteString("        proxy_request_buffering off;\n")
		sb.WriteString("        proxy_ignore_client_abort on;\n")
		sb.WriteString("        proxy_next_upstream off;\n")
		sb.WriteString("        tcp_nodelay on;\n")
		sb.WriteString("        client_max_body_size 0;\n")
		sb.WriteString("        proxy_read_timeout 1h;\n")
		sb.WriteString("        proxy_send_timeout 1h;\n")
		sb.WriteString("        proxy_set_header Host $host;\n")
		sb.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
		sb.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
		sb.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
		fmt.Fprintf(&sb, "        access_log %s/probe.log combined if=$log_probe;\n", opts.LogDir)
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSWS)] {
		wsPath := opts.Transport.WSPath()
		fmt.Fprintf(&sb, "    location = %s {\n", wsPath)
		sb.WriteString("        proxy_pass http://127.0.0.1:10001;\n")
		sb.WriteString("        proxy_http_version 1.1;\n")
		sb.WriteString("        proxy_buffering off;\n")
		sb.WriteString("        proxy_request_buffering off;\n")
		sb.WriteString("        tcp_nodelay on;\n")
		sb.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
		sb.WriteString("        proxy_set_header Connection \"upgrade\";\n")
		sb.WriteString("        proxy_read_timeout 300s;\n")
		sb.WriteString("        proxy_set_header Host $host;\n")
		sb.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
		sb.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
		fmt.Fprintf(&sb, "        access_log %s/probe.log combined if=$log_probe;\n", opts.LogDir)
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSHTTPUpgrade)] {
		httpUpgradePath := opts.Transport.UpgradePath()
		fmt.Fprintf(&sb, "    location = %s {\n", httpUpgradePath)
		sb.WriteString("        proxy_pass http://127.0.0.1:10004;\n")
		sb.WriteString("        proxy_http_version 1.1;\n")
		sb.WriteString("        proxy_buffering off;\n")
		sb.WriteString("        proxy_request_buffering off;\n")
		sb.WriteString("        tcp_nodelay on;\n")
		sb.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
		sb.WriteString("        proxy_set_header Connection \"upgrade\";\n")
		sb.WriteString("        proxy_read_timeout 1h;\n")
		sb.WriteString("        proxy_set_header Host $host;\n")
		sb.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
		sb.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
		fmt.Fprintf(&sb, "        access_log %s/probe.log combined if=$log_probe;\n", opts.LogDir)
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSGRPC)] {
		fmt.Fprintf(&sb, "    location /%s/ {\n", opts.Transport.GRPCService())
		sb.WriteString("        grpc_pass grpc://127.0.0.1:10003;\n")
		sb.WriteString("        client_max_body_size 0;\n")
		sb.WriteString("        client_body_buffer_size 512k;\n")
		sb.WriteString("        grpc_buffer_size 512k;\n")
		sb.WriteString("        grpc_read_timeout 1h;\n")
		sb.WriteString("        grpc_send_timeout 1h;\n")
		sb.WriteString("        grpc_set_header Host $host;\n")
		sb.WriteString("        grpc_set_header X-Real-IP $remote_addr;\n")
		fmt.Fprintf(&sb, "        access_log %s/probe.log combined if=$log_probe;\n", opts.LogDir)
		sb.WriteString("    }\n")
	}

	return sb.String()
}

// GenerateStreamConfig splits TCP 443 by SNI: reality → 8443, FakeTLS → its instance, else → https. A tls domain equal to the reality SNI is an error.
func GenerateStreamConfig(opts GeneratorOptions) (string, error) {
	opts.normalize()
	sni := strings.ToLower(strings.TrimSpace(opts.RealityServerName))
	tlsDomain := strings.ToLower(strings.TrimSpace(opts.MTProtoTLSDomain))

	if tlsDomain != "" && tlsDomain == sni {
		return "", fmt.Errorf("mtproto tls domain %q collides with the reality SNI: one SNI can hold only one map key", tlsDomain)
	}

	var mapBody strings.Builder
	if sni != "" {
		fmt.Fprintf(&mapBody, "        %s 127.0.0.1:%d;\n", sni, RealityInboundPort)
	}
	if tlsDomain != "" {
		fmt.Fprintf(&mapBody, "        %s 127.0.0.1:%d;\n", tlsDomain, MTProtoTLSInboundPort)
	}
	fmt.Fprintf(&mapBody, "        default 127.0.0.1:%d;\n", NginxHTTPSBackendPort)

	body := `# SNI splitter: TCP 443 is fanned out by TLS server name. Reality (sing-box), the optional MTProxy FakeTLS branch and the https server share one public port; UDP 443 stays with Hysteria2 - the stream module is TCP only.
stream {
    map $ssl_preread_server_name $uncut_tls_backend {
` + mapBody.String() + `    }

    server {
        listen 0.0.0.0:443;
        listen [::]:443;
        ssl_preread on;
        proxy_pass $uncut_tls_backend;
    }
}
`
	return body, nil
}

func GenerateServerConfig(opts GeneratorOptions) string {
	opts.normalize()
	domain := opts.Domain
	installDir := opts.InstallDir
	telegramPort := opts.TelegramProxyPort

	// packet-up + keepalive: no fresh TCP dial per POST; must live at http level.
	upstreamBlock := ""
	for _, p := range config.ResolveProtocols(opts.ActiveProtocols) {
		if strings.TrimSpace(p) == string(config.ProtoXHTTPStealth) {
			upstreamBlock = `upstream xhttp_backend {
    server 127.0.0.1:10002;
    keepalive 64;
}

`
			break
		}
	}

	// HTTP-01 needs nginx up, but the ssl server needs a cert — render 443 only once the files exist; Maintain re-renders after issuance.
	certful := true
	if _, err := os.Stat(filepath.Join(installDir, "certs", "certificates", domain+".crt")); err != nil {
		certful = false
	}
	if _, err := os.Stat(filepath.Join(installDir, "certs", "certificates", domain+".key")); err != nil {
		certful = false
	}

	var sb strings.Builder
	if err := serverTemplate.Execute(&sb, serverData{
		XHTTPUpstream: upstreamBlock != "",
		Cert:          certful,
		Domain:        domain,
		WebRoot:       opts.WebRoot,
		LogDir:        opts.LogDir,
		InstallDir:    installDir,
		SubLocation:   opts.Transport.SubLocation(),
		SubsZone:      subsZoneName,
		SubsDir:       opts.SubsDir,
		DoHPath:       opts.Transport.DoHPath(),
		TelegramPort:  telegramPort,
	}); err != nil {
		panic(fmt.Errorf("render server template: %w", err))
	}
	return sb.String()
}

// serverTemplate renders the whole server config: the xhttp upstream and the 80/8442 head always; the 443 server only once the certificate files exist.
var serverTemplate = template.Must(template.New("uncut").Parse(`{{if .XHTTPUpstream}}upstream xhttp_backend {
    server 127.0.0.1:10002;
    keepalive 64;
}

{{end}}map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

map $status $log_probe {
    ~^4    1;
    default 0;
}

server {
    listen 80 default_server;
    server_name _;
    server_tokens off;
    return 301 https://$host$request_uri;
}

server {
    listen 80;
    server_name {{.Domain}};
    server_tokens off;

    location ^~ /.well-known/acme-challenge/ {
        root {{.WebRoot}};
        allow all;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 127.0.0.1:8442 ssl default_server;
    server_name _;
    server_tokens off;
    ssl_reject_handshake on;
}

{{if .Cert}}server {
    # TCP 443 is owned by the stream splitter; nginx sits on loopback, no QUIC (UDP 443 belongs to Hysteria2).
    listen 127.0.0.1:8442 ssl;
    http2 on;
    server_name {{.Domain}};
    server_tokens off;

    access_log {{.LogDir}}/access.log;
    access_log {{.LogDir}}/probe.log combined if=$log_probe;
    error_log {{.LogDir}}/error.log warn;

    ssl_certificate {{.InstallDir}}/certs/certificates/{{.Domain}}.crt;
    ssl_certificate_key {{.InstallDir}}/certs/certificates/{{.Domain}}.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1h;
    ssl_session_tickets on;
    ssl_stapling on;
    ssl_stapling_verify on;
    # OCSP stapling needs a resolver to fetch issuer status; without it nginx logs warnings and serves no stapled response.
    resolver 1.1.1.1 8.8.8.8 valid=300s;
    resolver_timeout 5s;

    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Request-Id $request_id always;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffers 8 64k;
    proxy_buffer_size 128k;
    client_body_buffer_size 512k;
    client_max_body_size 1m;

    location ~ "{{.SubLocation}}" {
        limit_req zone={{.SubsZone}} burst=20 nodelay;
        alias {{.SubsDir}}/$1;
        default_type "application/octet-stream";
        add_header Content-Disposition "inline";
        add_header Cache-Control "private, no-cache, no-store, must-revalidate" always;
    }

    location = {{.DoHPath}} {
        limit_req zone={{.SubsZone}} burst=20 nodelay;
        proxy_pass http://127.0.0.1:3053/dns-query;
        proxy_buffering off;
    }

    include {{.InstallDir}}/nginx_locations.conf;

    location /api/v1/ {
        proxy_pass http://127.0.0.1:{{.TelegramPort}};
        # tproxy's own error page must not stand out from any other miss.
        proxy_intercept_errors on;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        client_max_body_size 0;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # tproxy takes one address and rejects a list, so the appending form would make a client arriving with its own header lose the bridge.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }

    location = / {
        # proxy_set_header at location level only; bridge if-block inherits them: Host pins tproxy, X-Forwarded-For stays single-address.
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        if ($arg_bridge != "") {
            proxy_pass http://127.0.0.1:{{.TelegramPort}};
            break;
        }
        return 404;
    }

    # Nothing is published; a stock 404 is the norm and doesn't correlate nodes.
    location / {
        return 404;
    }
}
{{end}}`))

// serverData carries every named field the server template renders; the named form kills the positional-argument bug class the Sprintf chain had.
type serverData struct {
	XHTTPUpstream bool
	Cert          bool
	Domain        string
	WebRoot       string
	LogDir        string
	InstallDir    string
	SubLocation   string
	SubsZone      string
	SubsDir       string
	DoHPath       string
	TelegramPort  int
}

func write(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
