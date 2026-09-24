package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/example/ssl-auto-renew/internal/acme"
	"github.com/example/ssl-auto-renew/internal/config"
	"github.com/example/ssl-auto-renew/internal/notify"
	"github.com/example/ssl-auto-renew/internal/store"
)

type RenewOptions struct {
	Force bool
	Name  string
	Now   time.Time
}

type Issuer interface {
	Issue(context.Context, acme.Request) (acme.Result, error)
}

type IssuerFunc func(context.Context, acme.Request) (acme.Result, error)

func (f IssuerFunc) Issue(ctx context.Context, req acme.Request) (acme.Result, error) {
	return f(ctx, req)
}

type Service struct {
	cfg    config.Config
	mu     sync.Mutex
	issuer Issuer
}

func New(cfg config.Config) *Service { return NewWithIssuer(cfg, IssuerFunc(acme.Issue)) }

func NewWithIssuer(cfg config.Config, issuer Issuer) *Service {
	return &Service{cfg: cfg, issuer: issuer}
}

func (s *Service) Renew(ctx context.Context, opts RenewOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	statePath := filepath.Join(s.cfg.Storage.DataDir, "state.json")
	state, err := store.Load(statePath)
	if err != nil {
		return err
	}
	var firstErr error
	for _, item := range s.cfg.Certificates {
		if opts.Name != "" && opts.Name != item.Name {
			continue
		}
		err := s.renewOne(ctx, item, opts)
		runState := state.Certificates[item.Name]
		runState.LastRunAt = opts.Now
		if err != nil {
			runState.LastResult = "failed"
			runState.LastError = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", item.Name, err)
			}
			_ = notify.Send(ctx, notify.Config{WebhookURLEnv: s.cfg.Notifications.WebhookURLEnv}, notify.Event{Certificate: item.Name, Operation: "renew", Result: "failed", Error: err.Error(), OccurredAt: opts.Now})
		} else {
			runState.LastResult = "success"
			runState.LastError = ""
			_ = notify.Send(ctx, notify.Config{WebhookURLEnv: s.cfg.Notifications.WebhookURLEnv}, notify.Event{Certificate: item.Name, Operation: "renew", Result: "success", OccurredAt: opts.Now})
		}
		state.Certificates[item.Name] = runState
	}
	if err := store.Save(statePath, state); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Service) renewOne(ctx context.Context, item config.Certificate, opts RenewOptions) error {
	remaining, err := RemainingDays(item.Deploy.FullchainFile)
	if err == nil && !opts.Force && remaining > item.RenewBeforeDays {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && !opts.Force {
		return err
	}
	result, err := s.issuer.Issue(ctx, acme.Request{DirectoryURL: s.cfg.CA.DirectoryURL, Email: s.cfg.CA.Email, AccountKey: s.cfg.CA.AccountKey, Domains: item.Domains, Challenge: item.Challenge, DNSProvider: item.DNSProvider, KeyType: item.KeyType})
	if err != nil {
		return err
	}
	return deploy(s.cfg.Storage.CertificateDir, item, result)
}

func deploy(root string, item config.Certificate, result acme.Result) error {
	version := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(root, item.Name, "versions", version)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create certificate version: %w", err)
	}
	for name, data := range map[string][]byte{"privkey.pem": result.PrivateKey, "cert.pem": result.Certificate, "fullchain.pem": result.FullChain} {
		if err := writePrivate(filepath.Join(dir, name), data); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	current := filepath.Join(root, item.Name, "current")
	tmp := current + ".next"
	_ = os.Remove(tmp)
	if err := os.Symlink(dir, tmp); err != nil {
		return fmt.Errorf("create current link: %w", err)
	}
	oldTarget, _ := os.Readlink(current)
	if err := os.Rename(tmp, current); err != nil {
		return fmt.Errorf("activate certificate: %w", err)
	}
	oldFullchain, oldFullchainErr := os.ReadFile(item.Deploy.FullchainFile)
	oldKey, oldKeyErr := os.ReadFile(item.Deploy.KeyFile)
	if err := copyFile(filepath.Join(dir, "fullchain.pem"), item.Deploy.FullchainFile, 0644); err != nil {
		return rollback(current, oldTarget, oldFullchain, oldFullchainErr, oldKey, oldKeyErr, item, err)
	}
	if err := copyFile(filepath.Join(dir, "privkey.pem"), item.Deploy.KeyFile, 0600); err != nil {
		return rollback(current, oldTarget, oldFullchain, oldFullchainErr, oldKey, oldKeyErr, item, err)
	}
	if item.Deploy.ReloadCommand != "" {
		cmd := exec.Command("sh", "-c", item.Deploy.ReloadCommand)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return rollback(current, oldTarget, oldFullchain, oldFullchainErr, oldKey, oldKeyErr, item, fmt.Errorf("reload command: %w", err))
		}
	}
	return nil
}

func rollback(current, oldTarget string, oldFullchain []byte, oldFullchainErr error, oldKey []byte, oldKeyErr error, item config.Certificate, cause error) error {
	if oldFullchainErr == nil {
		_ = os.WriteFile(item.Deploy.FullchainFile, oldFullchain, 0644)
	}
	if oldKeyErr == nil {
		_ = os.WriteFile(item.Deploy.KeyFile, oldKey, 0600)
	}
	if oldTarget != "" {
		_ = os.Remove(current)
		_ = os.Symlink(oldTarget, current)
	}
	return cause
}

func writePrivate(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func RemainingDays(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return 0, errors.New("certificate PEM block not found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, fmt.Errorf("parse certificate: %w", err)
	}
	return int(time.Until(cert.NotAfter).Hours() / 24), nil
}
