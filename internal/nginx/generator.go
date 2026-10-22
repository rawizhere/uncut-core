package nginx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

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
	Domain          string
	InstallDir      string
	SubsDir         string
	LogDir          string
	WebRoot         string
	ActiveProtocols []string
	Transport       *transport.Transport
	// RealityServerName: the SNI routed to the reality inbound, not https.
	RealityServerName string
	MTProtoTLSDomain  string
}

// conf.d always exists: the image installs nginx.org nginx.
const (
	confDir    = "/etc/nginx/conf.d"
	serverFile = confDir + "/uncut.conf"
	limitsFile = confDir + "/00-uncut-limits.conf"
)

func WriteFiles(opts GeneratorOptions) error {
	for _, dir := range []string{confDir, opts.SubsDir, opts.LogDir, opts.WebRoot, StreamConfDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := config.WriteFileAtomic(serverFile, []byte(GenerateServerConfig(opts)), 0o644); err != nil {
		return err
	}
	// The stream splitter lives at main context, outside the server conf.
	streamBody, err := GenerateStreamConfig(opts)
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(filepath.Join(StreamConfDir, StreamFileName), []byte(streamBody), 0o644); err != nil {
		return err
	}
	return writeLimits(limitsFile)
}

func writeLimits(path string) error {
	body := fmt.Sprintf("limit_req_zone $binary_remote_addr zone=%s:10m rate=%s;\n", subsZoneName, subsRate)

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), "zone="+subsZoneName) {
			return nil
		}
	}
	return config.WriteFileAtomic(path, []byte(body), 0o644)
}

// GenerateStreamConfig splits TCP 443 by SNI: reality → 8443, FakeTLS → its instance, else → https. A tls domain equal to the reality SNI is an error.
func GenerateStreamConfig(opts GeneratorOptions) (string, error) {
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
	active := make(map[string]bool, len(opts.ActiveProtocols))
	for _, p := range config.ResolveProtocols(opts.ActiveProtocols) {
		active[p] = true
	}

	// HTTP-01 needs nginx up, but the ssl server needs a cert — render 443 only once the files exist; Maintain re-renders after issuance.
	certful := true
	for _, ext := range []string{".crt", ".key"} {
		if _, err := os.Stat(filepath.Join(opts.InstallDir, "certs", "certificates", opts.Domain+ext)); err != nil {
			certful = false
		}
	}

	var sb strings.Builder
	if err := serverTemplate.Execute(&sb, serverData{
		XHTTP:        active[string(config.ProtoXHTTPStealth)],
		WS:           active[string(config.ProtoVLESSWS)],
		Upgrade:      active[string(config.ProtoVLESSHTTPUpgrade)],
		GRPC:         active[string(config.ProtoVLESSGRPC)],
		Cert:         certful,
		Domain:       opts.Domain,
		WebRoot:      opts.WebRoot,
		LogDir:       opts.LogDir,
		InstallDir:   opts.InstallDir,
		SubLocation:  opts.Transport.SubLocation(),
		SubsZone:     subsZoneName,
		SubsDir:      opts.SubsDir,
		DoHPath:      opts.Transport.DoHPath(),
		XHTTPPath:    opts.Transport.XHTTPPath(),
		WSPath:       opts.Transport.WSPath(),
		UpgradePath:  opts.Transport.UpgradePath(),
		GRPCService:  opts.Transport.GRPCService(),
		TelegramPort: config.DefaultTelegramProxyPort,
	}); err != nil {
		panic(fmt.Errorf("render server template: %w", err))
	}
	return sb.String()
}

// serverTemplate renders the whole server config: the 80/8442 head always; the 443 server, with a location per active protocol, only once the certificate files exist.
// The xhttp upstream (packet-up + keepalive: no fresh TCP dial per POST) must live at http level.
var serverTemplate = template.Must(template.New("uncut").Parse(`{{if .XHTTP}}upstream xhttp_backend {
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

{{if .XHTTP}}    location ^~ {{.XHTTPPath}} {
        proxy_pass http://xhttp_backend;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_ignore_client_abort on;
        proxy_next_upstream off;
        tcp_nodelay on;
        client_max_body_size 0;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        access_log {{.LogDir}}/probe.log combined if=$log_probe;
    }

{{end}}{{if .WS}}    location = {{.WSPath}} {
        proxy_pass http://127.0.0.1:10001;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        tcp_nodelay on;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        access_log {{.LogDir}}/probe.log combined if=$log_probe;
    }

{{end}}{{if .Upgrade}}    location = {{.UpgradePath}} {
        proxy_pass http://127.0.0.1:10004;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        tcp_nodelay on;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 1h;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        access_log {{.LogDir}}/probe.log combined if=$log_probe;
    }

{{end}}{{if .GRPC}}    location /{{.GRPCService}}/ {
        grpc_pass grpc://127.0.0.1:10003;
        client_max_body_size 0;
        client_body_buffer_size 512k;
        grpc_buffer_size 512k;
        grpc_read_timeout 1h;
        grpc_send_timeout 1h;
        grpc_set_header Host $host;
        grpc_set_header X-Real-IP $remote_addr;
        access_log {{.LogDir}}/probe.log combined if=$log_probe;
    }

{{end}}    location /api/v1/ {
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
	XHTTP, WS, Upgrade, GRPC bool
	Cert                     bool
	Domain                   string
	WebRoot                  string
	LogDir                   string
	InstallDir               string
	SubLocation              string
	SubsZone                 string
	SubsDir                  string
	DoHPath                  string
	XHTTPPath                string
	WSPath                   string
	UpgradePath              string
	GRPCService              string
	TelegramPort             string
}
