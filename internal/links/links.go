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

// MTProtoFakeTLSSecret: the ee tag (TLS transport, random padding) + raw 16-byte secret + hex of the impersonated domain.
func MTProtoFakeTLSSecret(raw, tlsDomain string) string {
	return "ee" + raw + hex.EncodeToString([]byte(tlsDomain))
}

// TMeProxyURL is the t.me frontend form of the FakeTLS proxy link.
func TMeProxyURL(fqdn, secret string) string {
	return fmt.Sprintf("https://t.me/proxy?server=%s&port=443&secret=%s", fqdn, secret)
}

// WebProxyURL is the tg://webproxy link for the tproxy bridge; the client derives the bridge capability locally from the secret.
// Secret form: raw 16-byte hex. The Android WEB Proxy field rejects the 0x70-marked base64 form (it validates exactly 16 bytes); hex works everywhere.
func WebProxyURL(domain, rawHex string) string {
	secret := strings.ToLower(strings.TrimSpace(rawHex))
	if _, err := hex.DecodeString(secret); err != nil || len(secret) != 32 {
		return ""
	}
	return fmt.Sprintf("tg://webproxy?server=%s&port=443&secret=%s", domain, secret)
}
