package qrcode

import (
	"testing"
)

func TestGeneratePNG(t *testing.T) {
	png, err := GeneratePNG("https://example.com/sub.bin", 256)
	if err != nil {
		t.Fatalf("GeneratePNG failed: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("GeneratePNG returned empty byte slice")
	}
}

func TestGenerateASCII(t *testing.T) {
	ascii, err := GenerateASCII("https://example.com/sub.bin")
	if err != nil {
		t.Fatalf("GenerateASCII failed: %v", err)
	}
	if len(ascii) == 0 {
		t.Fatal("GenerateASCII returned empty string")
	}
}
