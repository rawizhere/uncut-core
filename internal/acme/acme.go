package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/http/webroot"
	"github.com/go-acme/lego/v4/registration"
	"github.com/google/renameio/v2"
)

const (
	renewBefore        = 30 * 24 * time.Hour
	renewInterval      = 24 * time.Hour
	selfSignedValidity = 90 * 24 * time.Hour
)

var errNoCertificate = errors.New("no certificate")

type Manager struct {
	domain   string
	email    string
	certsDir string
	webRoot  string
	staging  bool
	eabKID   string
	eabHMAC  string
}

func New(domain, email, certsDir, webRoot string, staging bool, eabKID, eabHMAC string) *Manager {
	return &Manager{
		domain:   domain,
		email:    email,
		certsDir: certsDir,
		webRoot:  webRoot,
		staging:  staging,
		eabKID:   eabKID,
		eabHMAC:  eabHMAC,
	}
}

func (m *Manager) CertPath() string {
	return filepath.Join(m.certsDir, m.domain+".crt")
}

func (m *Manager) KeyPath() string {
	return filepath.Join(m.certsDir, m.domain+".key")
}

func (m *Manager) EnsureSelfSigned() (bool, error) {
	if fileExists(m.CertPath()) && fileExists(m.KeyPath()) {
		return false, nil
	}

	certPEM, keyPEM, err := selfSigned(m.domain)
	if err != nil {
		return false, err
	}

	if err := os.MkdirAll(m.certsDir, 0o755); err != nil {
		return false, fmt.Errorf("create certs dir: %w", err)
	}
	if err := writeFile(m.CertPath(), certPEM, 0o644); err != nil {
		return false, err
	}
	if err := writeFile(m.KeyPath(), keyPEM, 0o600); err != nil {
		return false, err
	}

	slog.Warn("Issued temporary self-signed certificate", "domain", m.domain)
	return true, nil
}

func (m *Manager) Ensure() (bool, error) {
	if m.valid() {
		return false, nil
	}

	if err := m.issue(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) Maintain(ctx context.Context, onRenew func()) {
	// Attempt issuance with initial exponential backoff
	backoff := 5 * time.Second
	for !m.valid() {
		issued, err := m.Ensure()
		if err == nil && issued {
			if onRenew != nil {
				onRenew()
			}
			break
		}
		if err != nil {
			slog.Warn("Certificate issuance failed, retrying in background...", "domain", m.domain, "backoff", backoff, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff *= 2
			if backoff > 10*time.Minute {
				backoff = 10 * time.Minute
			}
		}
	}

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if issued, err := m.Ensure(); err != nil {
				slog.Warn("Certificate renewal failed", "domain", m.domain, "error", err)
			} else if issued && onRenew != nil {
				onRenew()
			}
		}
	}
}

func (m *Manager) valid() bool {
	cert, err := m.parse()
	if err != nil {
		return false
	}
	if cert.Subject.String() == cert.Issuer.String() {
		return false
	}
	return time.Until(cert.NotAfter) > renewBefore
}

func (m *Manager) parse() (*x509.Certificate, error) {
	data, err := os.ReadFile(m.CertPath())
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errNoCertificate
	}
	return x509.ParseCertificate(block.Bytes)
}

func (m *Manager) issue() error {
	caEndpoints := []string{
		lego.LEDirectoryProduction,
	}
	if m.eabKID != "" && m.eabHMAC != "" {
		caEndpoints = []string{
			"https://acme.zerossl.com/v2/DV90",
			lego.LEDirectoryProduction,
		}
	}
	if m.staging {
		caEndpoints = []string{lego.LEDirectoryStaging}
	}

	var lastErr error
	for _, caURL := range caEndpoints {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return fmt.Errorf("generate account key: %w", err)
		}

		account := &user{email: m.email, key: key}
		cfg := lego.NewConfig(account)
		cfg.Certificate.KeyType = certcrypto.EC256
		cfg.CADirURL = caURL

		client, err := lego.NewClient(cfg)
		if err != nil {
			lastErr = err
			continue
		}

		provider, err := webroot.NewHTTPProvider(m.webRoot)
		if err != nil {
			return fmt.Errorf("create webroot provider: %w", err)
		}
		if err := client.Challenge.SetHTTP01Provider(provider); err != nil {
			return fmt.Errorf("set http-01 provider: %w", err)
		}

		var reg *registration.Resource
		if strings.Contains(caURL, "zerossl.com") && m.eabKID != "" && m.eabHMAC != "" {
			reg, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
				TermsOfServiceAgreed: true,
				Kid:                  m.eabKID,
				HmacEncoded:          m.eabHMAC,
			})
		} else {
			reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		}

		if err != nil {
			lastErr = err
			slog.Warn("ACME registration failed, trying next CA", "ca", caURL, "error", err)
			continue
		}
		account.reg = reg

		res, err := client.Certificate.Obtain(certificate.ObtainRequest{
			Domains: []string{m.domain},
			Bundle:  true,
		})
		if err != nil {
			lastErr = err
			slog.Warn("ACME directory attempt failed, trying fallback", "ca", caURL, "error", err)
			continue
		}

		if err := os.MkdirAll(m.certsDir, 0o755); err != nil {
			return fmt.Errorf("create certs dir: %w", err)
		}
		if err := writeFile(m.CertPath(), res.Certificate, 0o644); err != nil {
			return err
		}
		if err := writeFile(m.KeyPath(), res.PrivateKey, 0o600); err != nil {
			return err
		}

		slog.Info("Issued TLS certificate", "domain", m.domain, "ca", caURL)
		return nil
	}

	return fmt.Errorf("all ACME providers failed: %w", lastErr)
}

type user struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *user) GetEmail() string {
	return u.email
}

func (u *user) GetRegistration() *registration.Resource {
	return u.reg
}

func (u *user) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

func selfSigned(domain string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	tpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domain},
		DNSNames:              []string{domain},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
