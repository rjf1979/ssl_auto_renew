package main

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/ssl-admin/internal/alidns"
	"github.com/example/ssl-admin/internal/nginx"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type app struct {
	db       *sql.DB
	code     string
	sessions map[string]time.Time
	nginx    *nginx.Manager
	mu       sync.Mutex
}

type dnsRecord struct {
	ID         int64  `json:"id"`
	Domain     string `json:"domain"`
	ProviderID string `json:"providerId,omitempty"`
	Host       string `json:"host"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl"`
}
type managedDomain struct {
	ID      int64  `json:"id"`
	Domain  string `json:"domain"`
	Enabled bool   `json:"enabled"`
}
type certificateRequest struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Domains         []string `json:"domains"`
	Challenge       string   `json:"challenge"`
	DNSProvider     string   `json:"dnsProvider"`
	KeyType         string   `json:"keyType"`
	RenewBeforeDays int      `json:"renewBeforeDays"`
	Enabled         bool     `json:"enabled"`
}
type site struct {
	ID          int64  `json:"id"`
	Domain      string `json:"domain"`
	Upstream    string `json:"upstream"`
	Certificate string `json:"certificate"`
	Enabled     bool   `json:"enabled"`
}

func main() {
	dbPath := getenv("ADMIN_DB_PATH", "./data/admin.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := initDB(db); err != nil {
		panic(err)
	}
	a := &app{db: db, code: os.Getenv("ADMIN_AUTH_CODE"), sessions: map[string]time.Time{}, nginx: nginx.New()}
	if a.code == "" {
		slog.Warn("ADMIN_AUTH_CODE is not configured; login will be unavailable")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", a.login)
	mux.HandleFunc("/api/logout", a.logout)
	mux.HandleFunc("/api/session", a.session)
	mux.HandleFunc("/api/overview", a.protected(a.overview))
	mux.HandleFunc("/api/domains", a.protected(a.domains))
	mux.HandleFunc("/api/dns", a.protected(a.dns))
	mux.HandleFunc("/api/dns-sync", a.protected(a.syncDNS))
	mux.HandleFunc("/api/sites", a.protected(a.sites))
	mux.HandleFunc("/api/nginx-sync", a.protected(a.syncNginx))
	mux.HandleFunc("/api/certificates", a.protected(a.certificates))
	webDir := getenv("ADMIN_WEB_DIR", "./web/dist")
	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	addr := getenv("ADMIN_ADDR", "127.0.0.1:8080")
	slog.Info("admin server started", "addr", addr, "db", dbPath)
	if err := http.ListenAndServe(addr, withHeaders(mux)); err != nil {
		panic(err)
	}
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS domains (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS dns_records (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL DEFAULT '', host TEXT NOT NULL, type TEXT NOT NULL, value TEXT NOT NULL, ttl INTEGER NOT NULL DEFAULT 600);
CREATE TABLE IF NOT EXISTS sites (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL UNIQUE, upstream TEXT NOT NULL, certificate TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS certificate_requests (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, domains_json TEXT NOT NULL, challenge TEXT NOT NULL, dns_provider TEXT NOT NULL, key_type TEXT NOT NULL, renew_before_days INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS operations (id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, created_at TEXT NOT NULL);`)
	_, _ = db.Exec("ALTER TABLE dns_records ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE dns_records ADD COLUMN domain TEXT NOT NULL DEFAULT ''")
	base := getenv("ALI_DNS_DOMAIN", "askcode.cn")
	for _, domain := range configuredDomains() {
		_, _ = db.Exec("INSERT OR IGNORE INTO domains(domain,enabled) VALUES(?,1)", domain)
	}
	_, _ = db.Exec("UPDATE dns_records SET domain=? WHERE domain=''", base)
	return err
}

func configuredDomains() []string {
	values := []string{getenv("ALI_DNS_DOMAIN", "askcode.cn")}
	values = append(values, strings.Split(os.Getenv("ALI_DNS_DOMAINS"), ",")...)
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		domain := strings.ToLower(strings.TrimSpace(value))
		if domain != "" && validDomain(domain) && !seen[domain] {
			seen[domain] = true
			out = append(out, domain)
		}
	}
	return out
}

var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func validDomain(domain string) bool {
	return domainPattern.MatchString(domain) && !strings.Contains(domain, "..")
}

func (a *app) domains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query("SELECT id,domain,enabled FROM domains ORDER BY domain")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []managedDomain{}
		for rows.Next() {
			var item managedDomain
			var enabled int
			if rows.Scan(&item.ID, &item.Domain, &enabled) == nil {
				item.Enabled = enabled == 1
				out = append(out, item)
			}
		}
		writeJSON(w, out)
	case http.MethodPost:
		var input struct {
			Domain string `json:"domain"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, `{"error":"域名格式不正确"}`, 400)
			return
		}
		domain := strings.ToLower(strings.TrimSpace(input.Domain))
		if !validDomain(domain) {
			http.Error(w, `{"error":"请输入有效的根域名，例如 example.com"}`, 400)
			return
		}
		result, err := a.db.Exec("INSERT INTO domains(domain,enabled) VALUES(?,1)", domain)
		if err != nil {
			http.Error(w, "域名已存在或保存失败", 409)
			return
		}
		id, _ := result.LastInsertId()
		a.log("domain.create", domain)
		writeJSON(w, managedDomain{ID: id, Domain: domain, Enabled: true})
	default:
		methodNotAllowed(w)
	}
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || a.code == "" || input.Code != a.code {
		http.Error(w, `{"error":"授权码不正确"}`, http.StatusUnauthorized)
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		http.Error(w, "server error", 500)
		return
	}
	token := hex.EncodeToString(tokenBytes)
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(12 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "ssl_admin_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	writeJSON(w, map[string]bool{"authenticated": true})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("ssl_admin_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "ssl_admin_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, map[string]bool{"authenticated": false})
}
func (a *app) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"authenticated": a.authenticated(r)})
}
func (a *app) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authenticated(r) {
			http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
func (a *app) authenticated(r *http.Request) bool {
	c, err := r.Cookie("ssl_admin_session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expiry, ok := a.sessions[c.Value]
	return ok && expiry.After(time.Now())
}

func (a *app) overview(w http.ResponseWriter, r *http.Request) {
	var dns, sites, ops int
	_ = a.db.QueryRow("SELECT count(*) FROM dns_records").Scan(&dns)
	_ = a.db.QueryRow("SELECT count(*) FROM sites WHERE enabled = 1").Scan(&sites)
	_ = a.db.QueryRow("SELECT count(*) FROM operations").Scan(&ops)
	writeJSON(w, map[string]any{"dnsRecords": dns, "sites": sites, "certificates": 0, "operations": ops, "renewal": "由 sslctl 管理"})
}

func (a *app) dns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		domain := strings.TrimSpace(r.URL.Query().Get("domain"))
		query := "SELECT id,domain,provider_id,host,type,value,ttl FROM dns_records ORDER BY id DESC"
		args := []any{}
		if domain != "" {
			query = "SELECT id,domain,provider_id,host,type,value,ttl FROM dns_records WHERE domain=? ORDER BY id DESC"
			args = append(args, domain)
		}
		rows, err := a.db.Query(query, args...)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []dnsRecord{}
		for rows.Next() {
			var x dnsRecord
			_ = rows.Scan(&x.ID, &x.Domain, &x.ProviderID, &x.Host, &x.Type, &x.Value, &x.TTL)
			out = append(out, x)
		}
		writeJSON(w, out)
	case http.MethodPost:
		var x dnsRecord
		if json.NewDecoder(r.Body).Decode(&x) != nil || x.Host == "" || x.Type == "" || x.Value == "" {
			http.Error(w, `{"error":"请填写完整的 DNS 记录"}`, 400)
			return
		}
		if x.TTL == 0 {
			x.TTL = 600
		}
		if x.Domain == "" {
			x.Domain = getenv("ALI_DNS_DOMAIN", "askcode.cn")
		}
		if !a.isManagedDomain(x.Domain) {
			http.Error(w, `{"error":"请先添加并管理该根域名"}`, http.StatusConflict)
			return
		}
		provider, err := alidns.New(x.Domain)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		providerID, err := provider.Add(r.Context(), alidns.Record{Host: x.Host, Type: x.Type, Value: x.Value, TTL: x.TTL})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		res, err := a.db.Exec("INSERT INTO dns_records(domain,provider_id,host,type,value,ttl) VALUES(?,?,?,?,?,?)", x.Domain, providerID, x.Host, x.Type, x.Value, x.TTL)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		x.ID, _ = res.LastInsertId()
		a.log("dns.create", x.Host)
		writeJSON(w, x)
	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		var providerID, domain string
		if err := a.db.QueryRow("SELECT provider_id,domain FROM dns_records WHERE id=?", id).Scan(&providerID, &domain); err != nil {
			http.Error(w, "DNS 记录不存在", http.StatusNotFound)
			return
		}
		provider, err := alidns.New(domain)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := provider.Delete(r.Context(), providerID); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, err = a.db.Exec("DELETE FROM dns_records WHERE id=?", id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.log("dns.delete", strconv.FormatInt(id, 10))
		writeJSON(w, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (a *app) syncDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		domain = getenv("ALI_DNS_DOMAIN", "askcode.cn")
	}
	if !a.isManagedDomain(domain) {
		http.Error(w, `{"error":"请先添加并管理该根域名"}`, http.StatusConflict)
		return
	}
	provider, err := alidns.New(domain)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	records, err := provider.List(context.WithoutCancel(r.Context()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM dns_records WHERE domain=?", domain); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, item := range records {
		if _, err = tx.Exec("INSERT INTO dns_records(domain,provider_id,host,type,value,ttl) VALUES(?,?,?,?,?,?)", domain, item.ProviderID, item.Host, item.Type, item.Value, item.TTL); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.log("dns.sync", domain)
	writeJSON(w, map[string]any{"count": len(records), "records": records})
}

func (a *app) sites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if err := a.syncNginxSites(); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		rows, err := a.db.Query("SELECT id,domain,upstream,certificate,enabled FROM sites ORDER BY id DESC")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []site{}
		for rows.Next() {
			var x site
			var enabled int
			_ = rows.Scan(&x.ID, &x.Domain, &x.Upstream, &x.Certificate, &enabled)
			x.Enabled = enabled == 1
			out = append(out, x)
		}
		writeJSON(w, out)
	case http.MethodPost:
		var x site
		if json.NewDecoder(r.Body).Decode(&x) != nil || x.Domain == "" || x.Upstream == "" {
			http.Error(w, `{"error":"域名和上游地址不能为空"}`, 400)
			return
		}
		if x.Certificate == "" {
			x.Certificate = "askcode-wildcard"
		}
		if !a.dnsMatchesDomain(x.Domain) {
			http.Error(w, `{"error":"请先同步或创建该域名对应的 DNS 解析"}`, http.StatusConflict)
			return
		}
		x.Upstream = normalizeUpstream(x.Upstream)
		if err := a.nginx.Apply(r.Context(), x.Domain, x.Upstream, x.Certificate); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, err := a.db.Exec(`INSERT INTO sites(domain,upstream,certificate,enabled) VALUES(?,?,?,1)
ON CONFLICT(domain) DO UPDATE SET upstream=excluded.upstream,certificate=excluded.certificate,enabled=1`, x.Domain, x.Upstream, x.Certificate)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = a.db.QueryRow("SELECT id FROM sites WHERE domain=?", x.Domain).Scan(&x.ID)
		x.Enabled = true
		a.log("site.create", x.Domain)
		writeJSON(w, x)
	default:
		methodNotAllowed(w)
	}
}

func (a *app) syncNginx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := a.syncNginxSites(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM sites WHERE enabled=1").Scan(&count)
	a.log("nginx.sync", getenv("NGINX_SITE_DIR", "/etc/nginx/conf.d"))
	writeJSON(w, map[string]int{"count": count})
}

func (a *app) syncNginxSites() error {
	items, err := a.nginx.List()
	if err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sites SET enabled=0"); err != nil {
		return err
	}
	for _, item := range items {
		if _, err = tx.Exec(`INSERT INTO sites(domain,upstream,certificate,enabled) VALUES(?,?,?,1)
ON CONFLICT(domain) DO UPDATE SET upstream=excluded.upstream,certificate=excluded.certificate,enabled=1`, item.Domain, item.Upstream, item.Certificate); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *app) dnsMatchesDomain(domain string) bool {
	var base string
	if rows, err := a.db.Query("SELECT domain FROM domains WHERE enabled=1"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var candidate string
			if rows.Scan(&candidate) == nil && (domain == candidate || strings.HasSuffix(domain, "."+candidate)) && len(candidate) > len(base) {
				base = candidate
			}
		}
	}
	if base == "" || !a.isManagedDomain(base) {
		return false
	}
	host := "@"
	if domain != base {
		host = strings.TrimSuffix(domain, "."+base)
	}
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM dns_records WHERE domain=? AND host=? AND type IN ('A','AAAA','CNAME')", base, host).Scan(&count)
	return count > 0
}

func (a *app) isManagedDomain(domain string) bool {
	var enabled int
	return a.db.QueryRow("SELECT enabled FROM domains WHERE domain=?", strings.ToLower(strings.TrimSpace(domain))).Scan(&enabled) == nil && enabled == 1
}

func normalizeUpstream(value string) string {
	if strings.Contains(value, "://") {
		return value
	}
	return "http://" + value
}
func (a *app) certificates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var input certificateRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || !certificateNamePattern.MatchString(input.Name) || len(input.Domains) == 0 {
			http.Error(w, `{"error":"请填写证书名称和至少一个域名"}`, 400)
			return
		}
		for i, domain := range input.Domains {
			input.Domains[i] = strings.ToLower(strings.TrimSpace(domain))
			if !validCertificateDomain(input.Domains[i]) {
				http.Error(w, `{"error":"证书域名格式不正确"}`, 400)
				return
			}
		}
		if input.Challenge == "" {
			input.Challenge = "dns-01"
		}
		if input.DNSProvider == "" {
			input.DNSProvider = "alidns"
		}
		if input.KeyType == "" {
			input.KeyType = "ecdsa"
		}
		if input.RenewBeforeDays == 0 {
			input.RenewBeforeDays = 30
		}
		domainsJSON, _ := json.Marshal(input.Domains)
		result, err := a.db.Exec(`INSERT INTO certificate_requests(name,domains_json,challenge,dns_provider,key_type,renew_before_days,enabled) VALUES(?,?,?,?,?,?,1)`, input.Name, string(domainsJSON), input.Challenge, input.DNSProvider, input.KeyType, input.RenewBeforeDays)
		if err != nil {
			http.Error(w, "证书名称已存在或保存失败", http.StatusConflict)
			return
		}
		input.ID, _ = result.LastInsertId()
		if err := a.writeCertificateManifest(); err != nil {
			_, _ = a.db.Exec("DELETE FROM certificate_requests WHERE id=?", input.ID)
			http.Error(w, err.Error(), 500)
			return
		}
		a.log("certificate.create", input.Name)
		input.Enabled = true
		writeJSON(w, input)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items := []map[string]any{}
	seen := map[string]bool{}
	certificateDir := getenv("SSL_CERTIFICATE_DIR", "/etc/ssl-auto-renew/certs")
	if entries, err := os.ReadDir(certificateDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(certificateDir, entry.Name(), "current", "fullchain.pem")
			if item, ok := certificateInfo(entry.Name(), []string{entry.Name()}, path, "sslctl"); ok {
				items = append(items, item)
				seen[path] = true
			}
		}
	}
	if sites, err := a.nginx.List(); err == nil {
		for _, site := range sites {
			if site.Fullchain == "" || seen[site.Fullchain] {
				continue
			}
			if item, ok := certificateInfo(site.Certificate, []string{site.Domain}, site.Fullchain, "Nginx 本地配置"); ok {
				items = append(items, item)
				seen[site.Fullchain] = true
			}
		}
	}
	rows, err := a.db.Query("SELECT id,name,domains_json,challenge,dns_provider,key_type,renew_before_days,enabled FROM certificate_requests WHERE enabled=1 ORDER BY name")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var request certificateRequest
			var domainsJSON string
			var enabled int
			if rows.Scan(&request.ID, &request.Name, &domainsJSON, &request.Challenge, &request.DNSProvider, &request.KeyType, &request.RenewBeforeDays, &enabled) != nil {
				continue
			}
			_ = json.Unmarshal([]byte(domainsJSON), &request.Domains)
			found := false
			for _, item := range items {
				if item["name"] == request.Name {
					found = true
					break
				}
			}
			if !found {
				items = append(items, map[string]any{"name": request.Name, "domains": request.Domains, "issuer": "", "status": "待申请", "remainingDays": 0, "managedBy": "sslctl", "request": request})
			}
		}
	}
	writeJSON(w, items)
}

var certificateNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validCertificateDomain(domain string) bool {
	if strings.HasPrefix(domain, "*.") {
		domain = strings.TrimPrefix(domain, "*.")
	}
	return validDomain(domain)
}

func (a *app) writeCertificateManifest() error {
	rows, err := a.db.Query("SELECT name,domains_json,challenge,dns_provider,key_type,renew_before_days FROM certificate_requests WHERE enabled=1 ORDER BY name")
	if err != nil {
		return err
	}
	defer rows.Close()
	type deployment struct {
		FullchainFile string `yaml:"fullchain_file"`
		KeyFile       string `yaml:"key_file"`
		ReloadCommand string `yaml:"reload_command"`
	}
	type item struct {
		Name            string     `yaml:"name"`
		Domains         []string   `yaml:"domains"`
		Challenge       string     `yaml:"challenge"`
		DNSProvider     string     `yaml:"dns_provider"`
		KeyType         string     `yaml:"key_type"`
		RenewBeforeDays int        `yaml:"renew_before_days"`
		Deploy          deployment `yaml:"deploy"`
	}
	manifest := struct {
		Certificates []item `yaml:"certificates"`
	}{}
	certDir := getenv("SSL_CERTIFICATE_DIR", "/etc/ssl-auto-renew/certs")
	for rows.Next() {
		var name, domainsJSON, challenge, provider, keyType string
		var renewDays int
		if err := rows.Scan(&name, &domainsJSON, &challenge, &provider, &keyType, &renewDays); err != nil {
			return err
		}
		var domains []string
		if err := json.Unmarshal([]byte(domainsJSON), &domains); err != nil {
			return err
		}
		base := filepath.Join(certDir, name, "current")
		manifest.Certificates = append(manifest.Certificates, item{Name: name, Domains: domains, Challenge: challenge, DNSProvider: provider, KeyType: keyType, RenewBeforeDays: renewDays, Deploy: deployment{FullchainFile: filepath.Join(base, "fullchain.pem"), KeyFile: filepath.Join(base, "privkey.pem"), ReloadCommand: "nginx -t && systemctl reload nginx"}})
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	path := getenv("SSL_CERTIFICATE_MANIFEST", "/etc/ssl-auto-renew/certificates.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func certificateInfo(name string, domains []string, path, managedBy string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{"name": name, "domains": domains, "issuer": "", "status": "文件缺失", "remainingDays": 0, "managedBy": managedBy, "fullchain": path}, false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return map[string]any{"name": name, "domains": domains, "issuer": "", "status": "证书格式错误", "remainingDays": 0, "managedBy": managedBy, "fullchain": path}, true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return map[string]any{"name": name, "domains": domains, "issuer": "", "status": "证书格式错误", "remainingDays": 0, "managedBy": managedBy, "fullchain": path}, true
	}
	days := int(time.Until(cert.NotAfter).Hours() / 24)
	status := "有效"
	if days < 0 {
		status = "已过期"
	} else if days <= 30 {
		status = "即将到期"
	}
	issuer := cert.Issuer.CommonName
	if issuer == "" && len(cert.Issuer.Organization) > 0 {
		issuer = cert.Issuer.Organization[0]
	}
	return map[string]any{"name": name, "domains": domains, "issuer": issuer, "status": status, "remainingDays": days, "managedBy": managedBy, "fullchain": path}, true
}
func (a *app) log(action, target string) {
	_, _ = a.db.Exec("INSERT INTO operations(action,target,result,created_at) VALUES(?,?,?,?)", action, target, "success", time.Now().UTC().Format(time.RFC3339))
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
