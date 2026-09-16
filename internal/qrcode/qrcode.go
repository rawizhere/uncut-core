package qrcode

import (
	"github.com/skip2/go-qrcode"
)

// GenerateASCII renders a QR as a small string grid (node TUI).
func GenerateASCII(content string) (string, error) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return qr.ToSmallString(false), nil
}
