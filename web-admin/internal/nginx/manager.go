package nginx

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Manager struct {
	Dir            string
	CertificateDir string
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func New() *Manager {
	return &Manager{Dir: getenv("NGINX_SITE_DIR", "/etc/nginx/conf.d"), CertificateDir: getenv("SSL_CERTIFICATE_DIR", "/etc/ssl-auto-renew/certs")}
}

func (m *Manager) Apply(ctx context.Context, domain, upstream, certificate string) error {
	if !safeName.MatchString(domain) || strings.Contains(domain, "..") {
		return fmt.Errorf("invalid site domain")
	}
	if !safeName.MatchString(certificate) {
		return fmt.Errorf("invalid certificate name")
	}
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("upstream must be an http(s) URL")
	}
	if err := os.MkdirAll(m.Dir, 0755); err != nil {
		return fmt.Errorf("create nginx site directory: %w", err)
	}
	certDir := filepath.Join(m.CertificateDir, certificate)
	fullchain := filepath.Join(certDir, "current", "fullchain.pem")
	key := filepath.Join(certDir, "current", "privkey.pem")
	if _, err := os.Stat(fullchain); err != nil {
		return fmt.Errorf("certificate fullchain not found: %w", err)
	}
	if _, err := os.Stat(key); err != nil {
		return fmt.Errorf("certificate key not found: %w", err)
	}

	config := fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name %s;
    ssl_certificate %s;
    ssl_certificate_key %s;
    location / {
        proxy_pass %s;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`, domain, domain, fullchain, key, strings.TrimRight(upstream, "/"))
	path := filepath.Join(m.Dir, "ssl-admin-"+domain+".conf")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(config), 0644); err != nil {
		return fmt.Errorf("write nginx site: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("activate nginx site: %w", err)
	}
	if err := run(ctx, "nginx", "-t"); err != nil {
		return err
	}
	return run(ctx, "systemctl", "reload", "nginx")
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
