package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rawizhere/uncut-core/internal/db"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGetPublicIPv4FromEndpoints_Success(t *testing.T) {
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("198.51.100.42\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	ctx := context.Background()
	ip, err := GetPublicIPv4FromEndpoints(ctx, client, "https://mock.ip.endpoint")
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed: %v", err)
	}

	if ip != "198.51.100.42" {
		t.Errorf("Expected 198.51.100.42, got %s", ip)
	}
}

func TestGetPublicIPv4FromEndpoints_Fallback(t *testing.T) {
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "bad.endpoint" {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewBufferString("error")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("203.0.113.10\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	ctx := context.Background()
	ip, err := GetPublicIPv4FromEndpoints(ctx, client, "https://bad.endpoint", "https://good.endpoint")
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed on fallback: %v", err)
	}

	if ip != "203.0.113.10" {
		t.Errorf("Expected 203.0.113.10, got %s", ip)
	}
}

func TestSyncServerIP(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ipsync_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}
	defer func() { _ = store.Close() }()

	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("192.0.2.1")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	ctx := context.Background()
	ip, err := GetPublicIPv4FromEndpoints(ctx, client, "https://mock.endpoint")
	if err != nil {
		t.Fatalf("GetPublicIPv4FromEndpoints failed: %v", err)
	}

	_ = store.SetSetting("server_ip", "10.0.0.1")
	_ = store.SetSetting("server_ip", ip)
	val, err := store.GetSetting("server_ip")
	if err != nil || val != "192.0.2.1" {
		t.Errorf("Expected 192.0.2.1 in store, got %s, err: %v", val, err)
	}
}
