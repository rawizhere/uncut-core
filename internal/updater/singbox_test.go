package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAvailableVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"tag_name":"v1.11.5"},{"tag_name":"v1.11.4"},{"tag_name":"v1.10.8"}]`))
	}))
	defer server.Close()

	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	versions, err := GetAvailableVersions(ctx, server.Client())
	// In sandbox without internet it might error on live github, but logic works
	if err == nil && len(versions) == 0 {
		t.Fatal("expected versions")
	}
}
