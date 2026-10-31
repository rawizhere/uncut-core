package core

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/rawizhere/uncut-core/internal/config"
	"github.com/rawizhere/uncut-core/internal/db"
	"golang.org/x/crypto/curve25519"
)

// randomWGKeyPair returns a clamped curve25519 private key and its public key, both std base64 like wireguard tooling.
func randomWGKeyPair() (string, string, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", "", fmt.Errorf("read random key material: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("derive public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// RandomAWGSecret returns 32 random bytes as std base64: preshared keys and header protection keys.
func RandomAWGSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// EnsureAWGServerKeys generates the AmneziaWG server pair and header protection key once.
func EnsureAWGServerKeys(store *db.Store) error {
	priv, _ := store.GetSetting("awg_private_key")
	pub, _ := store.GetSetting("awg_public_key")
	header, _ := store.GetSetting("awg_header_protection_key")
	if priv != "" && pub != "" && header != "" {
		return nil
	}
	priv, pub, err := randomWGKeyPair()
	if err != nil {
		return err
	}
	if err := store.SetSettings(map[string]string{
		"awg_private_key":           priv,
		"awg_public_key":            pub,
		"awg_header_protection_key": RandomAWGSecret(),
	}); err != nil {
		return fmt.Errorf("save amneziawg server keys: %w", err)
	}
	slog.Info("Generated AmneziaWG server key pair")
	return nil
}

// BackfillClientAWG gives every client without AmneziaWG material a key pair, a PSK and the next free 10.8.0.x/32 address.
func BackfillClientAWG(store *db.Store) error {
	clients, err := store.GetClients()
	if err != nil {
		return fmt.Errorf("fetch clients: %w", err)
	}
	used := make(map[string]bool, len(clients))
	for _, c := range clients {
		if c.AWG != nil && c.AWG.IP != "" {
			used[c.AWG.IP] = true
		}
	}
	for i := range clients {
		c := &clients[i]
		if c.AWG != nil && c.AWG.PrivKey != "" && c.AWG.PubKey != "" && c.AWG.PSK != "" && c.AWG.IP != "" {
			continue
		}
		if c.AWG == nil {
			c.AWG = &config.ClientAWG{}
		}
		if c.AWG.PrivKey == "" || c.AWG.PubKey == "" {
			priv, pub, err := randomWGKeyPair()
			if err != nil {
				return err
			}
			c.AWG.PrivKey, c.AWG.PubKey = priv, pub
		}
		if c.AWG.PSK == "" {
			c.AWG.PSK = RandomAWGSecret()
		}
		if c.AWG.IP == "" {
			ip, err := nextAWGAddress(used)
			if err != nil {
				return err
			}
			c.AWG.IP = ip
			used[ip] = true
		}
		if err := store.AddClient(*c); err != nil {
			return fmt.Errorf("backfill awg keys for %s: %w", c.Name, err)
		}
		slog.Info("Generated AmneziaWG peer keys", "client", c.Name, "address", c.AWG.IP)
	}
	return nil
}

// nextAWGAddress picks the smallest free host address under the server's .1, up to .254.
func nextAWGAddress(used map[string]bool) (string, error) {
	for n := 2; n < 255; n++ {
		ip := fmt.Sprintf("10.8.0.%d/32", n)
		if !used[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("no free address in %s", "10.8.0.0/24")
}
