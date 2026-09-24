package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certificate"
	lego "github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns/alidns"
	"github.com/go-acme/lego/v5/registration"
)

type Request struct {
	DirectoryURL, Email, AccountKey string
	Domains                         []string
	Challenge, DNSProvider, KeyType string
}
type Result struct {
	PrivateKey, Certificate, FullChain []byte
	NotAfter                           time.Time
}
type user struct {
	Email string
	key   crypto.Signer
	reg   *acme.ExtendedAccount
}

func (u *user) GetEmail() string                       { return u.Email }
func (u *user) GetRegistration() *acme.ExtendedAccount { return u.reg }
func (u *user) GetPrivateKey() crypto.Signer           { return u.key }

func Issue(ctx context.Context, req Request) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if req.Challenge != "dns-01" || req.DNSProvider != "alidns" {
		return Result{}, fmt.Errorf("MVP currently supports only alidns DNS-01")
	}
	key, err := loadOrCreateKey(req.AccountKey)
	if err != nil {
		return Result{}, err
	}
	u := &user{Email: req.Email, key: key}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = req.DirectoryURL
	lc, err := lego.NewClient(cfg)
	if err != nil {
		return Result{}, fmt.Errorf("create ACME client: %w", err)
	}
	provider, err := alidns.NewDNSProvider()
	if err != nil {
		return Result{}, fmt.Errorf("create Alibaba Cloud DNS provider: %w", err)
	}
	if err := lc.Challenge.SetDNS01Provider(provider); err != nil {
		return Result{}, fmt.Errorf("configure DNS-01 provider: %w", err)
	}
	reg, err := lc.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return Result{}, fmt.Errorf("register ACME account: %w", err)
	}
	u.reg = reg
	certKey, err := newCertificateKey(req.KeyType)
	if err != nil {
		return Result{}, err
	}
	res, err := lc.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: req.Domains, PrivateKey: certKey, Bundle: true})
	if err != nil {
		return Result{}, fmt.Errorf("obtain certificate: %w", err)
	}
	block, _ := pem.Decode(res.Certificate)
	if block == nil {
		return Result{}, fmt.Errorf("CA returned invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Result{}, err
	}
	fullChain := append(append([]byte(nil), res.Certificate...), res.IssuerCertificate...)
	return Result{PrivateKey: pemEncodePrivateKey(certKey), Certificate: res.Certificate, FullChain: fullChain, NotAfter: cert.NotAfter}, nil
}

func loadOrCreateKey(path string) (*ecdsa.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			return x509.ParseECPrivateKey(block.Bytes)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	data, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data}), 0600)
}
func newCertificateKey(kind string) (crypto.Signer, error) {
	if kind == "rsa" {
		return rsa.GenerateKey(rand.Reader, 2048)
	}
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
func pemEncodePrivateKey(key crypto.PrivateKey) []byte {
	var data []byte
	var typ string
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		data, _ = x509.MarshalECPrivateKey(k)
		typ = "EC PRIVATE KEY"
	case *rsa.PrivateKey:
		data = x509.MarshalPKCS1PrivateKey(k)
		typ = "RSA PRIVATE KEY"
	}
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: data})
}
