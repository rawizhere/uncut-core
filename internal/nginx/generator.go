package nginx

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/renameio/v2"
	"github.com/rawizhere/uncut-core/internal/config"
)

const (
	subsZoneName   = "anti_subs"
	subsRate       = "10r/s"
	ingestZoneName = "anti_ingest"
	ingestRate     = "5r/s"
)

type GeneratorOptions struct {
	Domain            string
	InstallDir        string
	SubsDir           string
	LogDir            string
	WebRoot           string
	ProtocolSalt      string
	ActiveProtocols   []string
	APIVersion        string
	Region            string
	Regions           []string
	TelegramProxyPort int
}

type Paths struct {
	IncludeDir string
	SiteFile   string
	Locations  string
	LimitsFile string
}

func (opts *GeneratorOptions) normalize() {
	if opts.InstallDir == "" {
		opts.InstallDir = "/opt/sing-box"
	}
	if opts.ProtocolSalt == "" {
		opts.ProtocolSalt = "default"
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
	if opts.APIVersion == "" {
		opts.APIVersion = "2.4.1"
	}
	if opts.Region == "" {
		opts.Region = config.DefaultRegion
	}
	if len(opts.Regions) == 0 {
		opts.Regions = []string{opts.Region}
	}
	if opts.TelegramProxyPort <= 0 {
		opts.TelegramProxyPort = 8080
	}
}

func DetectPaths(installDir string) Paths {
	paths := Paths{
		Locations: filepath.Join(installDir, "nginx_locations.conf"),
	}

	switch {
	case dirExists("/etc/nginx/http.d"):
		paths.IncludeDir = "/etc/nginx/http.d"
		paths.SiteFile = "/etc/nginx/http.d/uncut.conf"
	case dirExists("/etc/nginx/sites-enabled"):
		paths.IncludeDir = "/etc/nginx/sites-enabled"
		paths.SiteFile = "/etc/nginx/sites-available/uncut"
	default:
		paths.IncludeDir = "/etc/nginx/conf.d"
		paths.SiteFile = "/etc/nginx/conf.d/uncut.conf"
	}

	paths.LimitsFile = filepath.Join(paths.IncludeDir, "00-uncut-limits.conf")
	return paths
}

func WriteFiles(opts GeneratorOptions, paths Paths) error {
	opts.normalize()

	docsDir := filepath.Join(opts.WebRoot, "docs")
	for _, dir := range []string{paths.IncludeDir, opts.SubsDir, opts.LogDir, opts.WebRoot, docsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := write(paths.Locations, []byte(GenerateLocationsConfig(opts)), 0o644); err != nil {
		return err
	}
	if err := write(paths.SiteFile, []byte(GenerateSiteConfig(opts)), 0o644); err != nil {
		return err
	}
	if err := writeLimits(paths.LimitsFile); err != nil {
		return err
	}

	published := publishedAt(opts.Domain)

	assets := map[string]string{
		filepath.Join(opts.WebRoot, "index.html"):  GenerateLandingHTML(opts),
		filepath.Join(opts.WebRoot, "favicon.svg"): GenerateFaviconSVG(),
		filepath.Join(opts.WebRoot, "robots.txt"):  "User-agent: *\nDisallow: /docs/\n",
		filepath.Join(docsDir, "openapi.json"):     GenerateOpenAPISpec(opts),
		filepath.Join(docsDir, "index.html"):       GenerateSwaggerHTML(opts),
	}
	for path, body := range assets {
		if err := write(path, []byte(body), 0o644); err != nil {
			return err
		}
		if err := os.Chtimes(path, published, published); err != nil {
			return fmt.Errorf("backdate %s: %w", path, err)
		}
	}

	if filepath.Dir(paths.SiteFile) != paths.IncludeDir {
		return linkSiteFile(paths)
	}
	return nil
}

func publishedAt(domain string) time.Time {
	sum := sha256.Sum256([]byte(domain))
	days := 20 + int(sum[0])%90
	return time.Now().AddDate(0, 0, -days)
}

func writeLimits(path string) error {
	body := fmt.Sprintf("limit_req_zone $binary_remote_addr zone=%s:10m rate=%s;\nlimit_req_zone $binary_remote_addr zone=%s:10m rate=%s;\n",
		subsZoneName, subsRate, ingestZoneName, ingestRate)

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), "zone="+subsZoneName) &&
			strings.Contains(string(existing), "zone="+ingestZoneName) {
			return nil
		}
	}
	return write(path, []byte(body), 0o644)
}

func linkSiteFile(paths Paths) error {
	link := filepath.Join(paths.IncludeDir, "uncut")
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	if err := os.Symlink(paths.SiteFile, link); err != nil {
		return fmt.Errorf("enable site: %w", err)
	}
	return nil
}

func GenerateLocationsConfig(opts GeneratorOptions) string {
	opts.normalize()
	active := make(map[string]bool, len(opts.ActiveProtocols))
	for _, p := range config.ResolveProtocols(opts.ActiveProtocols) {
		active[strings.TrimSpace(p)] = true
	}

	salt := opts.ProtocolSalt
	var sb strings.Builder

	if active[string(config.ProtoXHTTPStealth)] {
		xhttpPath := fmt.Sprintf("/v1/ingest/push/live-%s", salt)
		fmt.Fprintf(&sb, "    location ^~ %s {\n", xhttpPath)
		sb.WriteString("        proxy_pass http://127.0.0.1:10002;\n")
		sb.WriteString("        proxy_http_version 1.1;\n")
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
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSWS)] {
		wsPath := fmt.Sprintf("/v1/streams/live-%s/ws", salt)
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
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSHTTPUpgrade)] {
		httpUpgradePath := fmt.Sprintf("/v1/streams/live-%s/upgrade", salt)
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
		sb.WriteString("    }\n\n")
	}

	if active[string(config.ProtoVLESSGRPC)] {
		sb.WriteString("    location /ingest.v1.IngestService/ {\n")
		sb.WriteString("        grpc_pass grpc://127.0.0.1:10003;\n")
		sb.WriteString("        client_max_body_size 0;\n")
		sb.WriteString("        client_body_buffer_size 512k;\n")
		sb.WriteString("        grpc_buffer_size 512k;\n")
		sb.WriteString("        grpc_read_timeout 1h;\n")
		sb.WriteString("        grpc_send_timeout 1h;\n")
		sb.WriteString("        grpc_set_header Host $host;\n")
		sb.WriteString("        grpc_set_header X-Real-IP $remote_addr;\n")
		sb.WriteString("    }\n")
	}

	return sb.String()
}

func GenerateSiteConfig(opts GeneratorOptions) string {
	opts.normalize()
	domain := opts.Domain
	installDir := opts.InstallDir
	telegramPort := opts.TelegramProxyPort

	return fmt.Sprintf(`map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80 default_server;
    server_name _;
    server_tokens off;
    return 301 https://$host$request_uri;
}

server {
    listen 80;
    server_name %s;
    server_tokens off;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        allow all;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl default_server;
    server_name _;
    server_tokens off;
    ssl_reject_handshake on;
}

server {
    listen 443 ssl http2;
    server_name %s;
    server_tokens off;

    access_log %s/access.log;
    error_log %s/error.log warn;

    ssl_certificate %s/certs/certificates/%s.crt;
    ssl_certificate_key %s/certs/certificates/%s.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1h;
    ssl_session_tickets on;
    ssl_stapling on;
    ssl_stapling_verify on;

    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Request-Id $request_id always;
    add_header Alt-Svc 'h3=":443"; ma=86400' always;

    root %s;
    index index.html;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffers 8 64k;
    proxy_buffer_size 128k;
    client_body_buffer_size 512k;
    client_max_body_size 1m;

    location = /favicon.svg {
        log_not_found off;
        access_log off;
        try_files /favicon.svg =204;
    }

    location = /v1/health {
        default_type application/json;
        return 200 '{"status":"healthy","version":"%s","region":"%s","node_id":"%s","timestamp":"$time_iso8601","healthy_nodes":%d}\n';
    }

    location = /healthz {
        default_type text/plain;
        return 200 "ok\n";
    }

    location = /v1/telemetry/events {
        client_max_body_size 64k;
        limit_req zone=anti_ingest burst=10 nodelay;
        default_type application/json;
        if ($request_method = OPTIONS) {
            add_header Access-Control-Allow-Origin "*" always;
            add_header Access-Control-Allow-Methods "POST, OPTIONS" always;
            add_header Access-Control-Allow-Headers "Content-Type, Authorization, X-Stream-Key" always;
            add_header Content-Length 0;
            add_header Content-Type "text/plain";
            return 200;
        }
        if ($http_authorization = "") {
            return 401 '{"error":"unauthorized","message":"missing bearer token"}\n';
        }
        if ($request_method != POST) {
            return 405 '{"error":"method_not_allowed","message":"telemetry ingest requires POST"}\n';
        }
        return 202 '{"accepted":1,"batch_id":"$request_id","status":"queued"}\n';
    }

    location = /docs/openapi.json {
        default_type application/json;
        try_files /docs/openapi.json =404;
    }

    location /docs/ {
        try_files $uri $uri/ /docs/index.html =404;
    }

    location ~ "^/v1/schemas/([a-f0-9]{32})\.bin$" {
        limit_req zone=%s burst=20 nodelay;
        alias %s/$1;
        default_type "application/octet-stream";
        add_header Content-Disposition "inline";
        add_header Cache-Control "private, no-cache, no-store, must-revalidate" always;
    }

    include %s/nginx_locations.conf;

    location /api/v1/ {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        client_max_body_size 0;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }

    error_page 404 = @not_found;

    location @not_found {
        default_type application/json;
        return 404 '{"error":"Not Found","code":404}\n';
    }

    location = / {
        if ($arg_bridge != "") {
            proxy_pass http://127.0.0.1:%d;
            break;
        }
        try_files /index.html =404;
    }

    location / {
        if ($request_method = OPTIONS) {
            add_header Access-Control-Allow-Origin "*" always;
            add_header Access-Control-Allow-Methods "GET, HEAD, OPTIONS, POST" always;
            add_header Access-Control-Allow-Headers "*" always;
            add_header Content-Length 0;
            add_header Content-Type "text/plain";
            return 200;
        }

        try_files $uri $uri/ @not_found;
    }
}
`,
		domain,
		opts.WebRoot,
		domain,
		opts.LogDir, opts.LogDir,
		installDir, domain, installDir, domain,
		opts.WebRoot,
		opts.APIVersion, opts.Region, domain, len(opts.Regions),
		subsZoneName, opts.SubsDir,
		installDir,
		telegramPort,
		telegramPort,
	)
}

func GenerateFaviconSVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="6" fill="#0f172a"/><path d="M8 16h3l3-7 4 14 3-7h3" stroke="#38bdf8" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" fill="none"/></svg>`
}

func GenerateLandingHTML(opts GeneratorOptions) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Distributed Ingestion Network</title>
    <link rel="icon" type="image/svg+xml" href="/favicon.svg">
    <style>
        :root { --bg: #090d16; --surface: #111827; --border: #1f293d; --text: #e2e8f0; --muted: #94a3b8; --accent: #38bdf8; --green: #10b981; }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; background: var(--bg); color: var(--text); padding: 48px 24px; line-height: 1.5; font-size: 14px; }
        .container { max-width: 840px; margin: 0 auto; }
        .status-badge { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; color: var(--green); margin-bottom: 24px; }
        .status-dot { width: 8px; height: 8px; background: var(--green); border-radius: 50%%; box-shadow: 0 0 8px var(--green); }
        h1 { font-size: 20px; font-weight: 600; color: #fff; margin-bottom: 8px; }
        p.sub { color: var(--muted); margin-bottom: 32px; font-size: 13px; }
        .section { background: var(--surface); border: 1px solid var(--border); border-radius: 6px; padding: 20px; margin-bottom: 24px; }
        .section-title { font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--muted); margin-bottom: 14px; }
        table { width: 100%%; border-collapse: collapse; font-size: 13px; }
        th, td { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); }
        th { color: var(--muted); font-weight: 500; font-size: 11px; text-transform: uppercase; }
        td:last-child { text-align: right; }
        .method { display: inline-block; padding: 2px 6px; border-radius: 3px; font-size: 11px; font-weight: 600; }
        .get { background: #0369a1; color: #fff; }
        .post { background: #047857; color: #fff; }
        .grpc { background: #6d28d9; color: #fff; }
        .code-block { background: #040711; border: 1px solid var(--border); border-radius: 4px; padding: 14px; font-size: 12px; color: #cbd5e1; overflow-x: auto; }
        a { color: var(--accent); text-decoration: none; }
        a:hover { text-decoration: underline; }
        footer { margin-top: 48px; color: #475569; font-size: 12px; text-align: center; }
    </style>
</head>
<body>
    <div class="container">
        <div class="status-badge">
            <span class="status-dot"></span> Edge Node [%s] Operational &bull; Version %s
        </div>

        <h1>Distributed Stream & Telemetry Gateway</h1>
        <p class="sub">High-throughput multi-region ingestion network with bidirectional stream multiplexing.</p>

        <div class="section">
            <div class="section-title">Ingestion Fleet</div>
            <table>
                <thead>
                    <tr><th>Region</th><th>Transports</th><th>Status</th></tr>
                </thead>
                <tbody>
%s
                </tbody>
            </table>
        </div>

        <div class="section">
            <div class="section-title">Protocol Interfaces</div>
            <table>
                <thead>
                    <tr><th>Transport</th><th>Interface</th><th>Route</th></tr>
                </thead>
                <tbody>
                    <tr><td><span class="method post">POST</span></td><td>Batch Telemetry</td><td><code>/v1/telemetry/events</code></td></tr>
                    <tr><td><span class="method post">POST</span></td><td>Binary Stream Push</td><td><code>/v1/ingest/push/{streamKey}</code></td></tr>
                    <tr><td><span class="method get">GET</span></td><td>WebSocket Duplex</td><td><code>/v1/streams/{streamId}/ws</code></td></tr>
                    <tr><td><span class="method grpc">gRPC</span></td><td>Multiplexed Stream</td><td><code>/ingest.v1.IngestService/Stream</code></td></tr>
                </tbody>
            </table>
        </div>

        <div class="section">
            <div class="section-title">API Specifications</div>
            <p style="color:var(--muted);font-size:13px">Interactive API schemas and documentation available at <a href="/docs/">/docs/</a> or raw <a href="/docs/openapi.json">OpenAPI Spec (JSON)</a>.</p>
        </div>

        <footer>
            Node: %s &bull; Edge Infrastructure Gateway &bull; Auto-negotiated TLS 1.3 / HTTP/2
        </footer>
    </div>
</body>
</html>`, opts.Region, opts.APIVersion, fleetRows(opts), opts.Domain)
}

func fleetRows(opts GeneratorOptions) string {
	var sb strings.Builder
	for _, region := range opts.Regions {
		fmt.Fprintf(&sb, "                    <tr><td>%s</td><td>HTTP/2, QUIC, gRPC</td><td><span style=\"color:var(--green)\">Active</span></td></tr>\n", region)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func GenerateSwaggerHTML(opts GeneratorOptions) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Ingest API - Documentation</title>
    <link rel="icon" type="image/svg+xml" href="/favicon.svg">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace, sans-serif; margin: 0; padding: 0; background: #0f172a; color: #f8fafc; }
        .header { background: #1e293b; border-bottom: 1px solid #334155; padding: 20px 32px; display: flex; justify-content: space-between; align-items: center; }
        .title { font-size: 20px; font-weight: 700; color: #38bdf8; }
        .container { max-width: 1000px; margin: 32px auto; padding: 0 20px; }
        .card { background: #1e293b; border: 1px solid #334155; border-radius: 8px; margin-bottom: 20px; overflow: hidden; }
        .ep-header { padding: 16px 20px; display: flex; align-items: center; gap: 12px; font-family: monospace; font-size: 15px; border-bottom: 1px solid #334155; }
        .method { padding: 4px 10px; border-radius: 4px; font-weight: 700; font-size: 12px; }
        .get { background: #0284c7; color: #fff; }
        .post { background: #16a34a; color: #fff; }
        .grpc { background: #8b5cf6; color: #fff; }
        .path { font-weight: 600; color: #f8fafc; }
        .ep-body { padding: 16px 20px; font-size: 14px; color: #94a3b8; }
        .spec-link { font-size: 13px; color: #38bdf8; text-decoration: none; }
        .spec-link:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="header">
        <div class="title">Ingest API Specification</div>
        <div><a class="spec-link" href="/docs/openapi.json">OpenAPI Spec (JSON)</a></div>
    </div>
    <div class="container">
        <div class="card">
            <div class="ep-header"><span class="method get">GET</span><span class="path">/v1/health</span></div>
            <div class="ep-body">Edge gateway readiness and health status check. Returns regional availability telemetry.</div>
        </div>
        <div class="card">
            <div class="ep-header"><span class="method post">POST</span><span class="path">/v1/telemetry/events</span></div>
            <div class="ep-body">Asynchronous batch telemetry and client trace ingestion buffer. Returns 202 Accepted.</div>
        </div>
        <div class="card">
            <div class="ep-header"><span class="method get">GET</span><span class="path">/v1/streams/{streamId}/ws</span></div>
            <div class="ep-body">Full-duplex low-latency WebSocket live stream channel.</div>
        </div>
        <div class="card">
            <div class="ep-header"><span class="method post">POST</span><span class="path">/v1/ingest/push/{streamKey}</span></div>
            <div class="ep-body">High-throughput binary streaming pipeline for distributed ingestion. Accepts live stream token.</div>
        </div>
        <div class="card">
            <div class="ep-header"><span class="method grpc">gRPC</span><span class="path">/ingest.v1.IngestService/Stream</span></div>
            <div class="ep-body">Bidirectional multiplexed HTTP/2 gRPC streaming pipeline.</div>
        </div>
    </div>
</body>
</html>`
}

func GenerateOpenAPISpec(opts GeneratorOptions) string {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Ingest API",
			"description": "Multi-region low-latency stream ingestion, gRPC streaming, and telemetry gateway.",
			"version":     opts.APIVersion,
		},
		"servers": []map[string]any{
			{
				"url":         fmt.Sprintf("https://%s", opts.Domain),
				"description": fmt.Sprintf("Primary Ingest Node (%s)", opts.Region),
			},
		},
		"paths": map[string]any{
			"/v1/health": map[string]any{
				"get": map[string]any{
					"summary": "Health check",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Service is healthy and ready to accept streams",
						},
					},
				},
			},
			"/v1/telemetry/events": map[string]any{
				"post": map[string]any{
					"summary":     "Batch telemetry ingest",
					"description": "Queues client traces and metrics events.",
					"responses": map[string]any{
						"202": map[string]any{
							"description": "Events batch accepted and queued",
						},
						"405": map[string]any{
							"description": "Method Not Allowed - POST required",
						},
					},
				},
			},
			"/v1/streams/{streamId}/ws": map[string]any{
				"get": map[string]any{
					"summary":     "WebSocket telemetry stream channel",
					"description": "Establishes long-lived bidirectional streaming socket.",
					"parameters": []map[string]any{
						{
							"name":        "streamId",
							"in":          "path",
							"required":    true,
							"description": "Assigned stream authorization key",
							"schema": map[string]any{
								"type":    "string",
								"pattern": "^live-[a-f0-9]{8}$",
							},
						},
					},
					"responses": map[string]any{
						"101": map[string]any{
							"description": "Switching protocols to WebSocket stream",
						},
					},
				},
			},
			"/v1/ingest/push/{streamKey}": map[string]any{
				"post": map[string]any{
					"summary":     "Batch event stream push",
					"description": "Long-lived stream upload channel for high-throughput batch payloads.",
					"parameters": []map[string]any{
						{
							"name":        "streamKey",
							"in":          "path",
							"required":    true,
							"description": "Stream publishing token (e.g. live-a1b2c3d4)",
							"schema": map[string]any{
								"type":    "string",
								"pattern": "^live-[a-f0-9]{8}$",
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Stream buffer acknowledged",
						},
					},
				},
			},
			"/ingest.v1.IngestService/Stream": map[string]any{
				"post": map[string]any{
					"summary":     "gRPC bidirectional ingestion pipeline",
					"description": "Multiplexed gRPC protocol buffer channel for high-throughput telemetry frames.",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "gRPC stream status OK",
						},
					},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(spec, "", "  ")
	return string(data)
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
