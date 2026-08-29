package nginx

import (
	"strings"
	"testing"

	"github.com/rawizhere/uncut-core/internal/config"
)

func TestGenerateOpenAPISpec(t *testing.T) {
	opts := GeneratorOptions{
		Domain:     "ingest-eu-1.example.com",
		APIVersion: "2.4.1",
		Region:     "eu-1",
	}

	spec := GenerateOpenAPISpec(opts)
	if !strings.Contains(spec, "Ingest API") {
		t.Errorf("Spec missing title")
	}
	if !strings.Contains(spec, "2.4.1") {
		t.Errorf("Spec missing version")
	}
	if !strings.Contains(spec, "/v1/health") {
		t.Errorf("Spec missing /v1/health")
	}
	if !strings.Contains(spec, "/ingest.v1.IngestService/Stream") {
		t.Errorf("Spec missing /ingest.v1.IngestService/Stream")
	}
}

func TestGenerateLocationsConfig(t *testing.T) {
	opts := GeneratorOptions{
		Domain:       "ingest-eu-1.example.com",
		ProtocolSalt: "a1b2c3d4",
		ActiveProtocols: []string{
			string(config.ProtoXHTTPStealth),
			string(config.ProtoVLESSWS),
			string(config.ProtoVLESSHTTPUpgrade),
			string(config.ProtoVLESSGRPC),
		},
	}

	locs := GenerateLocationsConfig(opts)

	if !strings.Contains(locs, "location /v1/ingest/push/live-a1b2c3d4") {
		t.Errorf("Missing location /v1/ingest/push/live-a1b2c3d4")
	}
	if !strings.Contains(locs, "proxy_pass http://127.0.0.1:10002;") {
		t.Errorf("Missing proxy_pass 10002 for xhttp")
	}

	if !strings.Contains(locs, "location = /v1/streams/live-a1b2c3d4/ws") {
		t.Errorf("Missing location /v1/streams/live-a1b2c3d4/ws")
	}
	if !strings.Contains(locs, "proxy_pass http://127.0.0.1:10001;") {
		t.Errorf("Missing proxy_pass 10001 for ws")
	}

	if !strings.Contains(locs, "location = /v1/streams/live-a1b2c3d4/upgrade") {
		t.Errorf("Missing location /v1/streams/live-a1b2c3d4/upgrade")
	}
	if !strings.Contains(locs, "proxy_pass http://127.0.0.1:10004;") {
		t.Errorf("Missing proxy_pass 10004 for httpupgrade")
	}

	if !strings.Contains(locs, "location /ingest.v1.IngestService/") {
		t.Errorf("Missing location /ingest.v1.IngestService/")
	}
	if !strings.Contains(locs, "grpc_pass grpc://127.0.0.1:10003;") {
		t.Errorf("Missing grpc_pass 10003 for grpc")
	}
}

func TestGenerateSiteConfig(t *testing.T) {
	opts := GeneratorOptions{
		Domain:            "ingest-eu-1.example.com",
		InstallDir:        "/opt/sing-box",
		APIVersion:        "2.4.1",
		Region:            "eu-1",
		HealthUptime:      "4912",
		TelegramProxyPort: 8080,
	}

	siteConf := GenerateSiteConfig(opts)

	if !strings.Contains(siteConf, "server_name ingest-eu-1.example.com;") {
		t.Errorf("Site config missing server_name")
	}
	if !strings.Contains(siteConf, "ssl_reject_handshake on;") {
		t.Errorf("Site config missing ssl_reject_handshake on default server")
	}
	if !strings.Contains(siteConf, "add_header X-Request-Id $request_id always;") {
		t.Errorf("Site config missing X-Request-Id header")
	}
	if !strings.Contains(siteConf, "add_header X-Ingest-Region \"eu-1\" always;") {
		t.Errorf("Site config missing X-Ingest-Region header")
	}
	if !strings.Contains(siteConf, "add_header X-Ingest-Node \"ingest-eu-1.example.com\" always;") {
		t.Errorf("Site config missing X-Ingest-Node header")
	}
	if !strings.Contains(siteConf, "location = /v1/telemetry/events") {
		t.Errorf("Site config missing /v1/telemetry/events location")
	}
	if !strings.Contains(siteConf, "location = / {") {
		t.Errorf("Site config missing location = /")
	}
	if strings.Contains(siteConf, "/assets/js/([a-f0-9]{32})\\.bin$") {
		t.Errorf("Site config still contains legacy /assets/js location")
	}
	if !strings.Contains(siteConf, "location /api/v1/ {") {
		t.Errorf("Site config missing /api/v1/ location for telegram bridge")
	}
	if !strings.Contains(siteConf, "/v1/schemas/([a-f0-9]{32})\\.bin$") {
		t.Errorf("Site config missing /v1/schemas subscription location")
	}
}
