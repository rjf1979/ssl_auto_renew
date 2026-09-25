package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	CA                  CAConfig           `yaml:"ca"`
	Storage             StorageConfig      `yaml:"storage"`
	CertificateManifest string             `yaml:"certificate_manifest"`
	Certificates        []Certificate      `yaml:"certificates"`
	Notifications       NotificationConfig `yaml:"notifications"`
}

type CAConfig struct {
	DirectoryURL string `yaml:"directory_url"`
	Email        string `yaml:"email"`
	AccountKey   string `yaml:"account_key"`
}
type StorageConfig struct {
	DataDir        string `yaml:"data_dir"`
	CertificateDir string `yaml:"certificate_dir"`
}
type Certificate struct {
	Name            string     `yaml:"name"`
	Domains         []string   `yaml:"domains"`
	Challenge       string     `yaml:"challenge"`
	DNSProvider     string     `yaml:"dns_provider"`
	KeyType         string     `yaml:"key_type"`
	RenewBeforeDays int        `yaml:"renew_before_days"`
	Deploy          Deployment `yaml:"deploy"`
}
type Deployment struct {
	FullchainFile string `yaml:"fullchain_file"`
	KeyFile       string `yaml:"key_file"`
	ReloadCommand string `yaml:"reload_command"`
}
type NotificationConfig struct {
	WebhookURLEnv string `yaml:"webhook_url_env"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.CertificateManifest != "" {
		manifestData, err := os.ReadFile(cfg.CertificateManifest)
		if err != nil {
			return Config{}, fmt.Errorf("read certificate manifest: %w", err)
		}
		var manifest struct {
			Certificates []Certificate `yaml:"certificates"`
		}
		if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
			return Config{}, fmt.Errorf("parse certificate manifest: %w", err)
		}
		cfg.Certificates = manifest.Certificates
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.CA.DirectoryURL == "" || c.CA.Email == "" || c.CA.AccountKey == "" {
		return errors.New("ca.directory_url, ca.email and ca.account_key are required")
	}
	if c.Storage.DataDir == "" || c.Storage.CertificateDir == "" {
		return errors.New("storage.data_dir and storage.certificate_dir are required")
	}
	if len(c.Certificates) == 0 {
		return errors.New("at least one certificate is required")
	}
	seen := make(map[string]bool)
	for _, item := range c.Certificates {
		if item.Name == "" || seen[item.Name] {
			return fmt.Errorf("certificate name must be unique and non-empty: %q", item.Name)
		}
		seen[item.Name] = true
		if len(item.Domains) == 0 {
			return fmt.Errorf("certificate %q has no domains", item.Name)
		}
		if item.Challenge != "dns-01" && item.Challenge != "http-01" {
			return fmt.Errorf("certificate %q has unsupported challenge %q", item.Name, item.Challenge)
		}
		if item.Challenge == "dns-01" && item.DNSProvider == "" {
			return fmt.Errorf("certificate %q requires dns_provider", item.Name)
		}
		if item.KeyType != "ecdsa" && item.KeyType != "rsa" {
			return fmt.Errorf("certificate %q key_type must be ecdsa or rsa", item.Name)
		}
		if item.RenewBeforeDays < 1 || item.RenewBeforeDays > 89 {
			return fmt.Errorf("certificate %q renew_before_days must be between 1 and 89", item.Name)
		}
		if item.Deploy.FullchainFile == "" || item.Deploy.KeyFile == "" {
			return fmt.Errorf("certificate %q deployment files are required", item.Name)
		}
		if filepath.IsAbs(item.Deploy.FullchainFile) != filepath.IsAbs(item.Deploy.KeyFile) {
			return fmt.Errorf("certificate %q deployment paths must both be absolute or relative", item.Name)
		}
		for _, domain := range item.Domains {
			if strings.TrimSpace(domain) == "" || strings.ContainsAny(domain, " \t\r\n") {
				return fmt.Errorf("certificate %q contains invalid domain %q", item.Name, domain)
			}
		}
	}
	return nil
}
