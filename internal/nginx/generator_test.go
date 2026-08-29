package nginx

import (
	"strings"
	"testing"

	"github.com/rawizhere/uncut-core/internal/config"
)

func TestGenerateCDNHeadersSnippet(t *testing.T) {
	opts := GeneratorOptions{
		Domain:       "test.example.com",
		CFEdgeID:     "d1234567890abcd",
		CFPop:        "FRA50-C1",
		AWSReqID:     "test-aws-req-id",
		ServerHeader: "add_header Server \"CloudFront\" always;",
	}

	snippet := GenerateCDNHeadersSnippet(opts)

	if !strings.Contains(snippet, "Server \"CloudFront\"") {
		t.Errorf("Snippet missing Server CloudFront header")
	}
	if !strings.Contains(snippet, "Via \"1.1 d1234567890abcd.cloudfront.net (CloudFront)\"") {
		t.Errorf("Snippet missing Via header")
	}
	if !strings.Contains(snippet, "X-Amz-Cf-Pop \"FRA50-C1\"") {
		t.Errorf("Snippet missing X-Amz-Cf-Pop")
	}
	if !strings.Contains(snippet, "X-Amz-Cf-Id \"test-aws-req-id=\"") {
		t.Errorf("Snippet missing X-Amz-Cf-Id")
	}
}

func TestGenerateLocationsConfig(t *testing.T) {
	opts := GeneratorOptions{
		Domain:       "test.example.com",
		ProtocolSalt: "xyz987",
		ActiveProtocols: []string{
			string(config.ProtoXHTTPStealth),
			string(config.ProtoVLESSWS),
			string(config.ProtoVLESSGRPC),
		},
		AWSReqID: "req123",
		HostID:   "host123",
	}

	locs := GenerateLocationsConfig(opts)

	// xhttp-stealth should proxy to 10002
	if !strings.Contains(locs, "location ^~ /assets/js/xyz987") {
		t.Errorf("Missing location /assets/js/xyz987")
	}
	if !strings.Contains(locs, "proxy_pass http://127.0.0.1:10002;") {
		t.Errorf("Missing proxy_pass 10002 for xhttp")
	}

	// vless-ws should proxy to 10001
	if !strings.Contains(locs, "location ^~ /assets/css/xyz987") {
		t.Errorf("Missing location /assets/css/xyz987")
	}
	if !strings.Contains(locs, "proxy_pass http://127.0.0.1:10001;") {
		t.Errorf("Missing proxy_pass 10001 for ws")
	}

	// vless-httpupgrade is inactive, should return 403 S3 XML
	if !strings.Contains(locs, "location ^~ /assets/img/xyz987") {
		t.Errorf("Missing location /assets/img/xyz987")
	}
	if !strings.Contains(locs, "AccessDenied") {
		t.Errorf("Missing 403 S3 XML error for inactive httpupgrade")
	}

	// vless-grpc should grpc_pass to 10003
	if !strings.Contains(locs, "location ^~ /EdgeContent_xyz987") {
		t.Errorf("Missing location /EdgeContent_xyz987")
	}
	if !strings.Contains(locs, "grpc_pass grpc://127.0.0.1:10003;") {
		t.Errorf("Missing grpc_pass 10003 for grpc")
	}
}

func TestGenerateSiteConfig(t *testing.T) {
	opts := GeneratorOptions{
		Domain:            "cdn.test.com",
		InstallDir:        "/opt/sing-box",
		TelegramProxyPort: 8080,
	}

	siteConf := GenerateSiteConfig(opts)

	if !strings.Contains(siteConf, "server_name cdn.test.com;") {
		t.Errorf("Site config missing server_name")
	}
	if !strings.Contains(siteConf, "location /api/v1/ {") {
		t.Errorf("Site config missing Telegram proxy location /api/v1/")
	}
	if !strings.Contains(siteConf, "proxy_pass http://127.0.0.1:8080;") {
		t.Errorf("Site config missing Telegram proxy pass port 8080")
	}
	if !strings.Contains(siteConf, "/assets/js/([a-f0-9]{32})\\.bin$") {
		t.Errorf("Site config missing subscription regex location")
	}
}
