package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rawizhere/uncut-core/internal/db"
)

var DefaultIPEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me",
	"https://api.ip.sb/ip",
}

var ErrNoIPDetected = errors.New("failed to detect public IPv4 from all endpoints")

func GetPublicIPv4(ctx context.Context, client *http.Client) (string, error) {
	return GetPublicIPv4FromEndpoints(ctx, client, DefaultIPEndpoints...)
}

func GetPublicIPv4FromEndpoints(ctx context.Context, client *http.Client, endpoints ...string) (string, error) {
	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 3 * time.Second,
		}
	}

	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "curl/8.0.0")

		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}

		ipStr := strings.TrimSpace(string(body))
		parsedIP := net.ParseIP(ipStr)
		if parsedIP != nil && parsedIP.To4() != nil {
			return ipStr, nil
		}
	}

	return "", ErrNoIPDetected
}

func SyncServerIP(ctx context.Context, store *db.Store, client *http.Client, force bool) (string, bool, error) {
	currentIP, err := GetPublicIPv4(ctx, client)
	if err != nil {
		return "", false, fmt.Errorf("detect public ip: %w", err)
	}

	savedIP, _ := store.GetSetting("server_ip")
	if savedIP == "" {
		savedIP, _ = store.GetSetting("ip")
	}

	if force || savedIP != currentIP {
		if err := store.SetSetting("server_ip", currentIP); err != nil {
			return "", false, fmt.Errorf("save server_ip setting: %w", err)
		}
		if err := store.SetSetting("ip", currentIP); err != nil {
			return "", false, fmt.Errorf("save ip setting: %w", err)
		}

		slog.Info("Server IP synced", "previous", savedIP, "current", currentIP, "forced", force)
		return currentIP, true, nil
	}

	return currentIP, false, nil
}
