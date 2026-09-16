package tproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/links"
)

// bridgePageMarker: appears in the rendered page, never in the public stub.
const bridgePageMarker = "tproxy-v1."

const (
	bridgeProbeTimeout  = 5 * time.Second
	bridgeProbeAttempts = 20
	bridgeProbeBackoff  = 500 * time.Millisecond
	backendDialTimeout  = 3 * time.Second
)

// CheckBridge: a rejection answers 200 with the empty stub either way, so only
// the node can tell whether the bridge is real.
func CheckBridge(ctx context.Context, domain, secret string) error {
	capability := links.DeriveCapability(domain, secret)
	if capability == "" {
		return errors.New("mtproto secret is not 16 hex-encoded bytes")
	}

	target := "http://" + net.JoinHostPort("127.0.0.1", config.DefaultTelegramProxyPort) +
		"/?bridge=" + capability

	var lastErr error
	for attempt := 0; attempt < bridgeProbeAttempts; attempt++ {
		body, err := probeBridge(ctx, domain, target)
		if err == nil {
			if !strings.Contains(string(body), bridgePageMarker) {
				return fmt.Errorf("bridge served the public stub (%d bytes): capability rejected", len(body))
			}
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bridgeProbeBackoff):
		}
	}
	return fmt.Errorf("bridge unreachable on %s: %w", target, lastErr)
}

// CheckBackend: the page renders identically with a dead backend, so the
// symptom is invisible until a client actually connects.
func CheckBackend(ctx context.Context, backend string) error {
	if backend == "" {
		backend = DefaultBackend
	}
	d := net.Dialer{Timeout: backendDialTimeout}
	conn, err := d.DialContext(ctx, "tcp", backend)
	if err != nil {
		return fmt.Errorf("mtproxy backend %s is not accepting connections: %w", backend, err)
	}
	_ = conn.Close()
	return nil
}

func probeBridge(ctx context.Context, domain, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// The server answers only its own hostname and only for a loopback peer it can attribute to a single address.
	req.Host = domain
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	client := &http.Client{Timeout: bridgeProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
