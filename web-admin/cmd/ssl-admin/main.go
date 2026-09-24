package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/ssl-admin/internal/alidns"
	"github.com/example/ssl-admin/internal/nginx"
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
	ProviderID string `json:"providerId,omitempty"`
	Host       string `json:"host"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl"`
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
	mux.HandleFunc("/api/dns", a.protected(a.dns))
	mux.HandleFunc("/api/dns-sync", a.protected(a.syncDNS))
	mux.HandleFunc("/api/sites", a.protected(a.sites))
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
CREATE TABLE IF NOT EXISTS dns_records (id INTEGER PRIMARY KEY AUTOINCREMENT, host TEXT NOT NULL, type TEXT NOT NULL, value TEXT NOT NULL, ttl INTEGER NOT NULL DEFAULT 600);
CREATE TABLE IF NOT EXISTS sites (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL UNIQUE, upstream TEXT NOT NULL, certificate TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS operations (id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, created_at TEXT NOT NULL);`)
	_, _ = db.Exec("ALTER TABLE dns_records ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''")
	return err
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
		rows, err := a.db.Query("SELECT id,provider_id,host,type,value,ttl FROM dns_records ORDER BY id DESC")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		out := []dnsRecord{}
		for rows.Next() {
			var x dnsRecord
			_ = rows.Scan(&x.ID, &x.ProviderID, &x.Host, &x.Type, &x.Value, &x.TTL)
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
		provider, err := alidns.New()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		providerID, err := provider.Add(r.Context(), alidns.Record{Host: x.Host, Type: x.Type, Value: x.Value, TTL: x.TTL})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		res, err := a.db.Exec("INSERT INTO dns_records(provider_id,host,type,value,ttl) VALUES(?,?,?,?,?)", providerID, x.Host, x.Type, x.Value, x.TTL)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		x.ID, _ = res.LastInsertId()
		a.log("dns.create", x.Host)
		writeJSON(w, x)
	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		var providerID string
		if err := a.db.QueryRow("SELECT provider_id FROM dns_records WHERE id=?", id).Scan(&providerID); err != nil {
			http.Error(w, "DNS 记录不存在", http.StatusNotFound)
			return
		}
		provider, err := alidns.New()
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
	provider, err := alidns.New()
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
	if _, err = tx.Exec("DELETE FROM dns_records"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, item := range records {
		if _, err = tx.Exec("INSERT INTO dns_records(provider_id,host,type,value,ttl) VALUES(?,?,?,?,?)", item.ProviderID, item.Host, item.Type, item.Value, item.TTL); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.log("dns.sync", "askcode.cn")
	writeJSON(w, map[string]any{"count": len(records), "records": records})
}

func (a *app) sites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
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
		if err := a.nginx.Apply(r.Context(), x.Domain, normalizeUpstream(x.Upstream), x.Certificate); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		res, err := a.db.Exec("INSERT INTO sites(domain,upstream,certificate) VALUES(?,?,?)", x.Domain, x.Upstream, x.Certificate)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		x.ID, _ = res.LastInsertId()
		x.Enabled = true
		a.log("site.create", x.Domain)
		writeJSON(w, x)
	default:
		methodNotAllowed(w)
	}
}

func (a *app) dnsMatchesDomain(domain string) bool {
	base := getenv("ALI_DNS_DOMAIN", "askcode.cn")
	host := "@"
	if domain != base {
		if !strings.HasSuffix(domain, "."+base) {
			return false
		}
		host = strings.TrimSuffix(domain, "."+base)
	}
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM dns_records WHERE host=? AND type IN ('A','AAAA','CNAME')", host).Scan(&count)
	return count > 0
}

func normalizeUpstream(value string) string {
	if strings.Contains(value, "://") {
		return value
	}
	return "http://" + value
}
func (a *app) certificates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, []map[string]any{{"name": "askcode.cn wildcard", "domains": []string{"*.askcode.cn"}, "issuer": "Let's Encrypt", "status": "待配置", "remainingDays": 0, "managedBy": "sslctl"}})
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
