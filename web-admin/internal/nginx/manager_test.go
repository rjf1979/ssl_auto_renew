package nginx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListReadsLocalNginxSites(t *testing.T) {
	dir := t.TempDir()
	config := `server {
    server_name dns.askcode.cn;
    ssl_certificate /etc/letsencrypt/live/dns.askcode.cn/fullchain.pem;
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
server {
    server_name resume.askcode.cn;
    ssl_certificate /etc/ssl-auto-renew/certs/askcode-wildcard/current/fullchain.pem;
    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}`
	if err := os.WriteFile(filepath.Join(dir, "sites.conf"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	sites, err := (&Manager{Dir: dir}).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want 2", len(sites))
	}
	byDomain := make(map[string]Site)
	for _, item := range sites {
		byDomain[item.Domain] = item
	}
	if got := byDomain["dns.askcode.cn"].Certificate; got != "dns.askcode.cn" {
		t.Fatalf("certbot certificate = %q", got)
	}
	if got := byDomain["resume.askcode.cn"].Certificate; got != "askcode-wildcard" {
		t.Fatalf("shared certificate = %q", got)
	}
	if got := byDomain["resume.askcode.cn"].Upstream; got != "http://127.0.0.1:3000" {
		t.Fatalf("upstream = %q", got)
	}
}
