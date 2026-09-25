package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAcceptsDNS01Wildcard(t *testing.T) {
	cfg := Config{CA: CAConfig{DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory", Email: "a@example.com", AccountKey: "/tmp/account.key"}, Storage: StorageConfig{DataDir: "/tmp/data", CertificateDir: "/tmp/certs"}, Certificates: []Certificate{{Name: "example", Domains: []string{"example.com", "*.example.com"}, Challenge: "dns-01", DNSProvider: "alidns", KeyType: "ecdsa", RenewBeforeDays: 30, Deploy: Deployment{FullchainFile: "/tmp/fullchain.pem", KeyFile: "/tmp/privkey.pem"}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	cfg := Config{CA: CAConfig{DirectoryURL: "url", Email: "a@example.com", AccountKey: "key"}, Storage: StorageConfig{DataDir: "data", CertificateDir: "certs"}, Certificates: []Certificate{{Name: "same"}, {Name: "same"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate certificate names")
	}
}

func TestLoadUsesCertificateManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "certificates.yaml")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(manifest, []byte("certificates:\n  - name: one\n    domains: [example.com]\n    challenge: dns-01\n    dns_provider: alidns\n    key_type: ecdsa\n    renew_before_days: 30\n    deploy:\n      fullchain_file: /tmp/fullchain.pem\n      key_file: /tmp/privkey.pem\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := "ca:\n  directory_url: url\n  email: a@example.com\n  account_key: key\nstorage:\n  data_dir: " + filepath.Join(dir, "data") + "\n  certificate_dir: " + filepath.Join(dir, "certs") + "\ncertificate_manifest: " + manifest + "\ncertificates: []\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Certificates) != 1 || cfg.Certificates[0].Name != "one" {
		t.Fatalf("manifest certificates = %#v", cfg.Certificates)
	}
}
