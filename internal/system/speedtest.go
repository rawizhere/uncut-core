package system

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"time"
)

type SpeedtestResult struct {
	LatencyMS    float64
	DownloadMbps float64
	UploadMbps   float64
	TestEndpoint string
	TestedAt     time.Time
}

func RunSpeedtest(ctx context.Context) (*SpeedtestResult, error) {
	client := &http.Client{
		Timeout: 45 * time.Second,
	}

	endpoint := "https://speed.cloudflare.com"
	res := &SpeedtestResult{
		TestEndpoint: "Cloudflare Edge Anycast",
		TestedAt:     time.Now(),
	}

	// Latency measurement averaged across probes
	var totalLatency time.Duration
	probes := 3
	for i := 0; i < probes; i++ {
		start := time.Now()
		reqPing, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/__down?bytes=0", nil)
		if err == nil {
			if resp, err := client.Do(reqPing); err == nil {
				_ = resp.Body.Close()
				totalLatency += time.Since(start)
			}
		}
	}
	if probes > 0 {
		res.LatencyMS = float64(totalLatency.Microseconds()) / float64(probes*1000)
	}

	// Download test
	dlBytes := int64(50 * 1024 * 1024)
	reqDL, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/__down?bytes=%d", endpoint, dlBytes), nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	startDL := time.Now()
	respDL, err := client.Do(reqDL)
	if err != nil {
		return nil, fmt.Errorf("download test failed: %w", err)
	}

	buf := make([]byte, 64*1024)
	readBytes, err := io.CopyBuffer(io.Discard, respDL.Body, buf)
	_ = respDL.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read download stream: %w", err)
	}
	dlDuration := time.Since(startDL).Seconds()
	if dlDuration > 0 {
		res.DownloadMbps = (float64(readBytes*8) / (1024 * 1024)) / dlDuration
	}

	// Upload test
	ulBytes := int64(25 * 1024 * 1024)
	reqUL, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/__up", io.LimitReader(rand.Reader, ulBytes))
	if err != nil {
		return nil, fmt.Errorf("create upload request: %w", err)
	}
	reqUL.Header.Set("Content-Type", "application/octet-stream")

	startUL := time.Now()
	respUL, err := client.Do(reqUL)
	if err != nil {
		return nil, fmt.Errorf("upload test failed: %w", err)
	}
	_ = respUL.Body.Close()
	ulDuration := time.Since(startUL).Seconds()
	if ulDuration > 0 {
		res.UploadMbps = (float64(ulBytes*8) / (1024 * 1024)) / ulDuration
	}

	return res, nil
}
