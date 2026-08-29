package qrcode

import (
	"github.com/skip2/go-qrcode"
)

func GenerateASCII(content string) (string, error) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return qr.ToSmallString(false), nil
}
