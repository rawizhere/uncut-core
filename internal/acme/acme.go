package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/http/webroot"
	"github.com/go-acme/lego/v4/registration"
	"github.com/google/renameio/v2"
)

const (
	renewBefore   = 30 * 24 * time.Hour
	renewInterval = 24 * time.Hour
)

var errNoCertificate = errors.New("no certificate")

// Manager issues TLS certificates from Let's Encrypt over HTTP-01 (webroot) —
// the only CA. The ssl server renders only after the first certificate lands.
type Manager struct {
	domain   string
	email    string
	certsDir string
	webRoot  string
}

func New(domain, email, certsDir, webRoot string) *Manager {
	return &Manager{
		domain:   domain,
		email:    email,
		certsDir: certsDir,
		webRoot:  webRoot,
	}
}

func (m *Manager) CertPath() string {
	return filepath.Join(m.certsDir, m.domain+".crt")
}

func (m *Manager) KeyPath() string {
	return filepath.Join(m.certsDir, m.domain+".key")
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

func (m *Manager) ForceRenew() error {
	return m.issue()
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
	return time.Until(cert.NotAfter) > renewBefore
}

// Expires reports the certificate's expiry; the renewal window uses it.
func (m *Manager) Expires() (time.Time, error) {
	cert, err := m.parse()
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
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

func (m *Manager) getAccountKey() (crypto.PrivateKey, error) {
	keyPath := filepath.Join(m.certsDir, "acme_account.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate account key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err == nil {
		_ = os.MkdirAll(m.certsDir, 0o755)
		_ = writeFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	}
	return key, nil
}

func (m *Manager) issue() error {
	accountKey, err := m.getAccountKey()
	if err != nil {
		return err
	}

	email := m.email
	if email == "" {
		email = "admin@" + m.domain
	}
	account := &user{email: email, key: accountKey}
	cfg := lego.NewConfig(account)
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.CADirURL = lego.LEDirectoryProduction

	client, err := lego.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("create lego client: %w", err)
	}

	provider, err := webroot.NewHTTPProvider(m.webRoot)
	if err != nil {
		return fmt.Errorf("create webroot provider: %w", err)
	}
	if err := client.Challenge.SetHTTP01Provider(provider); err != nil {
		return fmt.Errorf("set http-01 provider: %w", err)
	}

	reg, err := client.Registration.ResolveAccountByKey()
	if err != nil {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		return fmt.Errorf("acme registration: %w", err)
	}
	account.reg = reg

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{m.domain},
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("certificate obtain: %w", err)
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

	slog.Info("Issued TLS certificate", "domain", m.domain, "ca", "letsencrypt")
	return nil
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

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
