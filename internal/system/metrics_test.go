package system

import (
	"context"
	"testing"
)

func TestGetHostMetrics(t *testing.T) {
	metrics := GetHostMetrics("")
	if metrics.UptimeFormatted == "" {
		t.Errorf("expected non-empty uptime")
	}
}

func TestRunSpeedtest(t *testing.T) {
	ctx := context.Background()
	res, err := RunSpeedtest(ctx)
	if err != nil {
		t.Logf("Speedtest skipped (network dependent in test env): %v", err)
		return
	}
	if res.LatencyMS <= 0 {
		t.Errorf("expected positive latency")
	}
}
