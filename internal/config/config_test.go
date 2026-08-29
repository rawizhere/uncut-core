package config

import (
	"testing"
	"time"
)

func TestValidProtocols(t *testing.T) {
	tests := []struct {
		proto string
		valid bool
	}{
		{"xhttp-stealth", true},
		{"vless-ws", true},
		{"vless-httpupgrade", true},
		{"vless-grpc", true},
		{"vless-reality", true},
		{"tuic", true},
		{"invalid-proto", false},
	}

	for _, tt := range tests {
		if got := IsValidProtocol(tt.proto); got != tt.valid {
			t.Errorf("IsValidProtocol(%q) = %v, want %v", tt.proto, got, tt.valid)
		}
	}
}

func TestGetMSKTime(t *testing.T) {
	msk := GetMSKTime()
	_, offset := msk.Zone()
	if offset != 3*60*60 {
		t.Errorf("GetMSKTime offset = %d, want %d", offset, 3*60*60)
	}
	if diff := time.Since(msk); diff > 2*time.Second || diff < -2*time.Second {
		t.Errorf("GetMSKTime() diverged from current time: %v", diff)
	}
}
