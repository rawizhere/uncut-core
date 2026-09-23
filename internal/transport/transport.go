// Package transport holds the concrete transport facts on the wire: random paths, gRPC name, reality SNI. Generated once, replaced only by rotation.
package transport

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// HashToken marks the per-client md5 inside the subscription path; generation data, never editable.
const HashToken = "{hash}"

const (
	DefaultGRPC = "ingest.v1.IngestService"
	// DefaultRealitySNI is the fallback SNI for the REALITY handshake.
	DefaultRealitySNI = "dl.google.com"
)

// SubHashPattern matches the subscription hash: md5, so 32 hex digits.
const SubHashPattern = "[a-f0-9]{32}"

// unsafeCharsRe: the reality SNI travels verbatim into nginx and sing-box.
var unsafeCharsRe = regexp.MustCompile(`[\s;$"'\\]`)

// Transport is the node's stored transport: concrete strings, not templates.
type Transport struct {
	GRPC    string `json:"grpc,omitempty"`
	XHTTP   string `json:"xhttp,omitempty"`
	WS      string `json:"ws,omitempty"`
	Upgrade string `json:"upgrade,omitempty"`
	// Sub is a generated wrapper around HashToken, e.g. "/a1b2.../{hash}.bin".
	Sub string `json:"sub,omitempty"`
	// RealityServerName is the public SNI the reality handshake masquerades as.
	RealityServerName string `json:"realityServerName,omitempty"`
}

// LegacyTemplates is the old template legend, read only during migration.
type LegacyTemplates struct {
	GRPC  string `json:"grpcService,omitempty"`
	Paths struct {
		XHTTP   string `json:"ingest,omitempty"`
		WS      string `json:"ws,omitempty"`
		Upgrade string `json:"upgrade,omitempty"`
		Sub     string `json:"sub,omitempty"`
	} `json:"paths,omitempty"`
	RealityServerName string `json:"realityServerName,omitempty"`
}

// Generate returns transport facts with freshly random paths, once per node.
func Generate() (*Transport, error) {
	t := &Transport{RealityServerName: DefaultRealitySNI}
	var err error
	if t.GRPC, err = randomSeg(); err != nil {
		return nil, err
	}
	if t.XHTTP, err = randomPath(); err != nil {
		return nil, err
	}
	if t.WS, err = randomPath(); err != nil {
		return nil, err
	}
	if t.Upgrade, err = randomPath(); err != nil {
		return nil, err
	}
	var prefix string
	if prefix, err = randomPath(); err != nil {
		return nil, err
	}
	t.Sub = prefix + "/" + HashToken + ".bin"
	return t, nil
}

// FromLegacy bakes the template legend + protocol salt into concrete paths; the wire bytes stay identical.
func FromLegacy(l *LegacyTemplates, salt string) *Transport {
	if salt == "" {
		// Callers used to fall back to this literal for an empty salt.
		salt = "default"
	}
	bake := func(s string) string { return strings.ReplaceAll(s, "{salt}", salt) }
	return (&Transport{
		GRPC:              l.GRPC,
		XHTTP:             bake(l.Paths.XHTTP),
		WS:                bake(l.Paths.WS),
		Upgrade:           bake(l.Paths.Upgrade),
		Sub:               l.Paths.Sub,
		RealityServerName: l.RealityServerName,
	}).fill()
}

// fill defaults facts migration may leave empty; paths never default — a default is an obvious path.
func (t *Transport) fill() *Transport {
	out := *t
	if out.GRPC == "" {
		out.GRPC = DefaultGRPC
	}
	if out.RealityServerName == "" {
		out.RealityServerName = DefaultRealitySNI
	}
	return &out
}

func (t *Transport) XHTTPPath() string   { return t.fill().XHTTP }
func (t *Transport) WSPath() string      { return t.fill().WS }
func (t *Transport) UpgradePath() string { return t.fill().Upgrade }
func (t *Transport) GRPCService() string { return t.fill().GRPC }

// RealitySNI returns the reality handshake SNI with the default applied.
func (t *Transport) RealitySNI() string { return t.fill().RealityServerName }

func (t *Transport) SubPath(hash string) string {
	return strings.ReplaceAll(t.fill().Sub, HashToken, safe(hash))
}

func (t *Transport) SubURL(domain, hash string) string {
	return "https://" + domain + t.SubPath(hash)
}

// SubLocation renders the nginx location that matches any per-client hash inside the stored subscription wrapper.
func (t *Transport) SubLocation() string {
	sub := t.fill().Sub
	prefix, suffix, found := strings.Cut(sub, HashToken)
	if !found {
		return "^" + regexp.QuoteMeta(sub) + "$"
	}
	return "^" + regexp.QuoteMeta(prefix) + "(" + SubHashPattern + ")" + regexp.QuoteMeta(suffix) + "$"
}

// DoHPath is the private DoH endpoint: the same random prefix the subscription wrapper uses, so one secret guards both.
func (t *Transport) DoHPath() string {
	prefix, _, _ := strings.Cut(t.fill().Sub, HashToken)
	return strings.TrimSuffix(prefix, "/") + "/dns-query"
}

// Rev fingerprints paths + gRPC + SNI — exactly the fields inside client links.
func (t *Transport) Rev() string {
	f := t.fill()
	parts := []string{f.XHTTP, f.WS, f.Upgrade, f.Sub, f.GRPC, f.RealityServerName}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])[:8]
}

// Validate checks the one transport fact an operator can type: the reality SNI.
func (t *Transport) Validate() error {
	if t != nil && t.RealityServerName != "" && unsafeCharsRe.MatchString(t.RealityServerName) {
		return fmt.Errorf("realityServerName: %q contains forbidden characters", t.RealityServerName)
	}
	return nil
}

// safe keeps stray characters in a hash out of nginx directives.
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return -1
		}
	}, s)
}

// randomSeg: one opaque alnum segment, 62^16 (~95 bits) — the single primitive behind every transport fact: "/"+seg, gRPC bare seg, sub seg+"/"+hash+".bin".
func randomSeg() (string, error) {
	seg, err := randomAlnum(16)
	if err != nil {
		return "", err
	}
	return seg, nil
}

func randomPath() (string, error) {
	seg, err := randomSeg()
	if err != nil {
		return "", err
	}
	return "/" + seg, nil
}

func randomAlnum(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}
