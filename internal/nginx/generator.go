package nginx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/renameio/v2"
	"github.com/rawizhere/uncut-core/internal/config"
)

var defaultPOPList = []string{
	"FRA50-C1", "IAD89-C2", "LHR61-C1", "NRT57-C2",
	"SIN52-C1", "SYD62-C2", "AMS54-C1", "CDG50-C1",
	"DFW55-C3", "HEL50-C2", "HKG54-C1", "JFK50-P3",
	"LAX50-P1", "MAD50-C1", "MIA50-C1", "SFO50-C1",
}

const (
	subsZoneName = "anti_subs"
	subsRate     = "10r/s"
)

type GeneratorOptions struct {
	Domain            string
	InstallDir        string
	SubsDir           string
	LogDir            string
	WebRoot           string
	ProtocolSalt      string
	ActiveProtocols   []string
	CFEdgeID          string
	CFPop             string
	AWSReqID          string
	HostID            string
	ServerHeader      string
	TelegramProxyPort int
}

type Paths struct {
	IncludeDir  string
	SiteFile    string
	SnippetsDir string
	CDNHeaders  string
	Locations   string
	LimitsFile  string
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
	if opts.CFEdgeID == "" {
		opts.CFEdgeID = "d" + randomHex(7)
	}
	if opts.CFPop == "" {
		opts.CFPop = RandomPOP()
	}
	if opts.AWSReqID == "" {
		opts.AWSReqID = randomBase62(54)
	}
	if opts.HostID == "" {
		opts.HostID = randomBase62(27)
	}
	if opts.ServerHeader == "" {
		opts.ServerHeader = "add_header Server \"CloudFront\" always;"
	}
	if opts.TelegramProxyPort <= 0 {
		opts.TelegramProxyPort = 8080
	}
}

func DetectPaths(installDir string) Paths {
	paths := Paths{
		SnippetsDir: "/etc/nginx/snippets",
		Locations:   filepath.Join(installDir, "nginx_locations.conf"),
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

	paths.CDNHeaders = filepath.Join(paths.SnippetsDir, "cdn_headers.conf")
	paths.LimitsFile = filepath.Join(paths.IncludeDir, "00-uncut-limits.conf")
	return paths
}

func WriteFiles(opts GeneratorOptions, paths Paths) error {
	opts.normalize()

	for _, dir := range []string{paths.SnippetsDir, paths.IncludeDir, opts.SubsDir, opts.LogDir, opts.WebRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := write(paths.CDNHeaders, []byte(GenerateCDNHeadersSnippet(opts)), 0o644); err != nil {
		return err
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

	if filepath.Dir(paths.SiteFile) != paths.IncludeDir {
		return linkSiteFile(paths)
	}
	return nil
}

func writeLimits(path string) error {
	zone := fmt.Sprintf("limit_req_zone $binary_remote_addr zone=%s:10m rate=%s;", subsZoneName, subsRate)

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), "zone="+subsZoneName) {
			return nil
		}
	}
	return write(path, []byte(zone+"\n"), 0o644)
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

func GenerateCDNHeadersSnippet(opts GeneratorOptions) string {
	opts.normalize()
	var sb strings.Builder
	sb.WriteString(opts.ServerHeader)
	sb.WriteString("\n")
	sb.WriteString("add_header X-Cache \"Hit from cloudfront\" always;\n")
	fmt.Fprintf(&sb, "add_header Via \"1.1 %s.cloudfront.net (CloudFront)\" always;\n", opts.CFEdgeID)
	sb.WriteString("add_header Accept-Ranges \"bytes\" always;\n")
	sb.WriteString("add_header Vary \"Accept-Encoding, Origin\" always;\n")
	sb.WriteString("add_header Age \"$cache_age\" always;\n")
	fmt.Fprintf(&sb, "add_header X-Amz-Cf-Id \"%s=\" always;\n", opts.AWSReqID)
	fmt.Fprintf(&sb, "add_header X-Amz-Cf-Pop \"%s\" always;\n", opts.CFPop)
	sb.WriteString("add_header Strict-Transport-Security \"max-age=31536000; includeSubDomains\" always;\n")
	sb.WriteString("add_header Alt-Svc 'h3=\":443\"; ma=86400' always;\n")
	return sb.String()
}

func GenerateLocationsConfig(opts GeneratorOptions) string {
	opts.normalize()
	active := make(map[string]bool)
	for _, p := range opts.ActiveProtocols {
		active[strings.TrimSpace(p)] = true
	}

	salt := opts.ProtocolSalt
	var sb strings.Builder

	xhttpPath := fmt.Sprintf("/assets/js/%s", salt)
	sb.WriteString("    # Masked Location: /assets/js\n")
	fmt.Fprintf(&sb, "    location ^~ %s {\n", xhttpPath)
	if active[string(config.ProtoXHTTPStealth)] {
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
		sb.WriteString("        add_header X-Amz-Cf-Id \"redacted\" always;\n")
		sb.WriteString("        add_header X-Edge-Origin-Shield \"active\" always;\n")
	} else {
		sb.WriteString(s3Denied(opts, "/assets/js"))
	}
	sb.WriteString("    }\n\n")

	wsPath := fmt.Sprintf("/assets/css/%s", salt)
	sb.WriteString("    # Masked Location: /assets/css\n")
	fmt.Fprintf(&sb, "    location ^~ %s {\n", wsPath)
	if active[string(config.ProtoVLESSWS)] {
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
		sb.WriteString("        add_header X-Amz-Cf-Id \"redacted\" always;\n")
		sb.WriteString("        add_header X-Edge-Origin-Shield \"active\" always;\n")
	} else {
		sb.WriteString(s3Denied(opts, "/assets/css"))
	}
	sb.WriteString("    }\n\n")

	httpUpgradePath := fmt.Sprintf("/assets/img/%s", salt)
	sb.WriteString("    # Masked Location: /assets/img\n")
	fmt.Fprintf(&sb, "    location ^~ %s {\n", httpUpgradePath)
	if active[string(config.ProtoVLESSHTTPUpgrade)] {
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
		sb.WriteString("        add_header X-Amz-Cf-Id \"redacted\" always;\n")
		sb.WriteString("        add_header X-Edge-Origin-Shield \"active\" always;\n")
	} else {
		sb.WriteString(s3Denied(opts, "/assets/img"))
	}
	sb.WriteString("    }\n\n")

	if active[string(config.ProtoVLESSGRPC)] {
		grpcPath := fmt.Sprintf("/EdgeContent_%s", salt)
		sb.WriteString("    # Masked Location: EdgeContent\n")
		fmt.Fprintf(&sb, "    location ^~ %s {\n", grpcPath)
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

func s3Denied(opts GeneratorOptions, resource string) string {
	var sb strings.Builder
	sb.WriteString("        include /etc/nginx/snippets/cdn_headers.conf;\n")
	sb.WriteString("        add_header x-amz-request-id \"$request_id\" always;\n")
	sb.WriteString("        default_type application/xml;\n")
	fmt.Fprintf(&sb, "        return 403 '<?xml version=\"1.0\" encoding=\"UTF-8\"?>\\n<Error>\\n    <Code>AccessDenied</Code>\\n    <Message>Access Denied</Message>\\n    <RequestId>%s</RequestId>\\n    <HostId>%s</HostId>\\n    <Resource>%s</Resource>\\n</Error>';\n", opts.AWSReqID, opts.HostID, resource)
	return sb.String()
}

func GenerateSiteConfig(opts GeneratorOptions) string {
	opts.normalize()
	domain := opts.Domain
	installDir := opts.InstallDir
	telegramPort := opts.TelegramProxyPort

	return fmt.Sprintf(`map $msec $cache_age {
    ~0$ "0";
    ~1$ "2";
    ~2$ "7";
    ~3$ "15";
    ~4$ "23";
    ~5$ "0";
    ~6$ "35";
    ~7$ "0";
    ~8$ "48";
    ~9$ "61";
    default "0";
}

map $http_upgrade $connection_upgrade {
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
    listen 443 ssl http2 default_server;
    server_name _;
    server_tokens off;
    ssl_certificate %s/certs/certificates/%s.crt;
    ssl_certificate_key %s/certs/certificates/%s.key;

    include /etc/nginx/snippets/cdn_headers.conf;
    add_header x-amz-request-id "%s" always;
    add_header x-amz-bucket-region "eu-central-1" always;
    default_type application/xml;

    return 403 '<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>AccessDenied</Code>
    <Message>Access Denied</Message>
    <RequestId>%s</RequestId>
    <HostId>%s</HostId>
    <Resource>/</Resource>
</Error>';
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
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1h;
    ssl_session_tickets on;
    ssl_stapling on;
    ssl_stapling_verify on;

    include /etc/nginx/snippets/cdn_headers.conf;

    add_header Access-Control-Allow-Origin "*" always;
    add_header Access-Control-Allow-Methods "GET, HEAD, OPTIONS" always;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffers 8 64k;
    proxy_buffer_size 128k;
    client_body_buffer_size 512k;
    client_max_body_size 0;

    location ~ "^/([a-f0-9]{32})$" {
        limit_req zone=%s burst=20 nodelay;
        alias %s/$1;
        default_type "application/octet-stream";
        add_header Content-Disposition "inline";
    }

    location ~ "^/assets/js/([a-f0-9]{32})\.bin$" {
        limit_req zone=%s burst=20 nodelay;
        alias %s/$1;
        default_type "application/octet-stream";
        add_header Content-Disposition "inline";
        add_header Cache-Control "private, no-cache, no-store, must-revalidate" always;
    }

    location /images/ {
        include /etc/nginx/snippets/cdn_headers.conf;
        root /var/www/cdn;
        expires 30d;
        try_files $uri =404;
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

    location @s3error {
        include /etc/nginx/snippets/cdn_headers.conf;
        add_header x-amz-request-id "%s" always;
        add_header x-amz-bucket-region "eu-central-1" always;
        default_type application/xml;
        return 403 '<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>AccessDenied</Code>
    <Message>Access Denied</Message>
    <RequestId>%s</RequestId>
    <HostId>%s</HostId>
    <Resource>$request_uri</Resource>
</Error>';
    }

    location / {
        if ($arg_bridge != "") {
            proxy_pass http://127.0.0.1:%d;
            break;
        }
        if ($http_upgrade = "websocket") {
            proxy_pass http://127.0.0.1:%d;
            break;
        }

        if ($request_method = OPTIONS) {
            add_header Access-Control-Allow-Origin "*" always;
            add_header Access-Control-Allow-Methods "GET, HEAD, OPTIONS, POST, PUT, DELETE" always;
            add_header Access-Control-Allow-Headers "*" always;
            add_header Content-Length 0;
            add_header Content-Type "text/plain";
            return 200;
        }

        error_page 403 = @s3error;
        return 403;
    }
}
`,
		domain,
		opts.WebRoot,
		installDir, domain, installDir, domain,
		opts.AWSReqID, opts.AWSReqID, opts.HostID,
		domain,
		opts.LogDir, opts.LogDir,
		installDir, domain, installDir, domain,
		subsZoneName, opts.SubsDir,
		subsZoneName, opts.SubsDir,
		installDir,
		telegramPort,
		opts.AWSReqID, opts.AWSReqID, opts.HostID,
		telegramPort, telegramPort,
	)
}

func RandomPOP() string {
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(defaultPOPList))))
	if err != nil {
		return defaultPOPList[0]
	}
	return defaultPOPList[idx.Int64()]
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

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomBase62(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[idx.Int64()]
	}
	return string(b)
}
