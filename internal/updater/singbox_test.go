package updater

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGetAvailableVersions(t *testing.T) {
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`[{"tag_name":"v1.11.5"},{"tag_name":"v1.11.4"},{"tag_name":"v1.10.8"}]`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	ctx := context.Background()
	versions, err := GetAvailableVersionsFromURL(ctx, client, "https://mock.github.api/releases")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	if versions[0] != "1.11.5" {
		t.Errorf("expected 1.11.5, got %s", versions[0])
	}
}
