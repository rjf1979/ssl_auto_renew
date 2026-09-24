package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/ssl-auto-renew/internal/acme"
	"github.com/example/ssl-auto-renew/internal/config"
)

func TestRenewSkipsCertificateOutsideWindow(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fullchain.pem")
	writeTestCertificate(t, certPath, 60*24*time.Hour)

	called := false
	issuer := IssuerFunc(func(context.Context, acme.Request) (acme.Result, error) {
		called = true
		return acme.Result{}, nil
	})
	cfg := config.Config{
		CA:           config.CAConfig{DirectoryURL: "https://example.invalid", Email: "admin@example.com", AccountKey: filepath.Join(dir, "account.key")},
		Storage:      config.StorageConfig{DataDir: dir, CertificateDir: filepath.Join(dir, "certs")},
		Certificates: []config.Certificate{{Name: "example", Domains: []string{"example.com"}, Challenge: "dns-01", DNSProvider: "alidns", KeyType: "ecdsa", RenewBeforeDays: 30, Deploy: config.Deployment{FullchainFile: certPath, KeyFile: filepath.Join(dir, "key.pem")}}},
	}
	if err := NewWithIssuer(cfg, issuer).Renew(context.Background(), RenewOptions{Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("issuer was called outside the renewal window")
	}
}

func writeTestCertificate(t *testing.T, path string, lifetime time.Duration) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(lifetime)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
}
