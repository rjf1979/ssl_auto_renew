package config

import "testing"

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
