package qrcode

import (
	"testing"
)

func TestGenerateASCII(t *testing.T) {
	ascii, err := GenerateASCII("https://example.com/sub.bin")
	if err != nil {
		t.Fatalf("GenerateASCII failed: %v", err)
	}
	if len(ascii) == 0 {
		t.Fatal("GenerateASCII returned empty string")
	}
}
