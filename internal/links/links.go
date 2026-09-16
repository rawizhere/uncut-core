// Package links turns the node's domain and MTProto secret into client URLs.
package links

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// DeriveCapability: HMAC-SHA256(domain, secret); unparseable secret yields "".
func DeriveCapability(domain, secretHex string) string {
	decoded, err := hex.DecodeString(strings.TrimSpace(secretHex))
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, decoded)
	_, _ = mac.Write([]byte("tdesktop-web-proxy-bridge-v1\n" + strings.ToLower(domain)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// BridgeURL: https + the node's domain + a capability only its secret produces.
func BridgeURL(domain, secretHex string) string {
	cap := DeriveCapability(domain, secretHex)
	if cap == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/?bridge=%s", domain, cap)
}

// MTProtoFakeTLSSecret: the ee tag (TLS transport, random padding) + raw
// 16-byte secret + hex of the impersonated domain.
func MTProtoFakeTLSSecret(raw, tlsDomain string) string {
	return "ee" + raw + hex.EncodeToString([]byte(tlsDomain))
}

// MTProtoURL: native-client link; the stream splitter owns 443, so port = 443.
func MTProtoURL(fqdn, secret string) string {
	return fmt.Sprintf("tg://proxy?server=%s&port=443&secret=%s", fqdn, secret)
}

// TMeProxyURL is the t.me frontend form of the FakeTLS proxy link.
func TMeProxyURL(fqdn, secret string) string {
	return fmt.Sprintf("https://t.me/proxy?server=%s&port=443&secret=%s", fqdn, secret)
}

// WebProxyMarkedSecret frames the raw MTProxy secret the tproxy deploy way:
// one 0x70 marker byte + the 16 raw bytes, base64url (their deploy rule).
func WebProxyMarkedSecret(rawHex string) string {
	decoded, err := hex.DecodeString(strings.TrimSpace(rawHex))
	if err != nil || len(decoded) != 16 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(append([]byte{0x70}, decoded...))
}

// WebProxyURL is the tg://webproxy link for the tproxy bridge; the client
// derives the bridge capability locally from the secret.
func WebProxyURL(domain, rawHex string) string {
	marked := WebProxyMarkedSecret(rawHex)
	if marked == "" {
		return ""
	}
	return fmt.Sprintf("tg://webproxy?server=%s&port=443&secret=%s", domain, marked)
}
