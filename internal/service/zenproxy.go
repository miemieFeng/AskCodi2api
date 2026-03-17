package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/util"
)

const (
	defaultZenProxyBase   = "http://cn.azt.cc:13000"
	defaultZenProxyAPIKey = "84bd5209-5f4a-4cc4-b053-7204ffca10b2"
	// Supabase test URL for proxy validation
	supabaseTestURL = "https://umnszlghpeqeuclzpjoy.supabase.co/auth/v1/health"
)

type ZenProxyService struct {
	db     *sqlx.DB
	logger *Logger
}

func NewZenProxyService(db *sqlx.DB, logger *Logger) *ZenProxyService {
	return &ZenProxyService{db: db, logger: logger}
}

type zenProxyItem struct {
	Name    string                 `json:"name"`
	Server  string                 `json:"server"`
	Port    float64                `json:"port"`
	Type    string                 `json:"type"`
	Quality map[string]interface{} `json:"quality"`
}

// FetchAndRefreshProxies fetches proxies from ZenProxy, validates them against Supabase, and updates the DB.
func (z *ZenProxyService) FetchAndRefreshProxies() {
	z.logger.Log("[ZenProxy] Starting proxy refresh...")

	proxies, err := z.fetchFromZenProxy()
	if err != nil {
		z.logger.Log(fmt.Sprintf("[ZenProxy] Failed to fetch proxies: %v", err))
		return
	}
	z.logger.Log(fmt.Sprintf("[ZenProxy] Fetched %d proxy candidates", len(proxies)))

	valid := 0
	for _, p := range proxies {
		proxyURL := z.buildProxyURL(p)
		if proxyURL == "" {
			continue
		}
		if !z.validateProxy(proxyURL) {
			continue
		}
		// Upsert into proxies table
		_, err := z.db.Exec(
			`INSERT INTO proxies (url, status, fail_count, created_at, updated_at)
			 VALUES (?, 'Active', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			 ON CONFLICT(url) DO UPDATE SET status='Active', fail_count=0, updated_at=CURRENT_TIMESTAMP`,
			proxyURL,
		)
		if err != nil {
			z.logger.Log(fmt.Sprintf("[ZenProxy] DB upsert error for %s: %v", proxyURL, err))
			continue
		}
		valid++
	}

	z.logger.Log(fmt.Sprintf("[ZenProxy] Proxy refresh done: %d valid proxies added/updated", valid))
}

func (z *ZenProxyService) fetchFromZenProxy() ([]zenProxyItem, error) {
	// Read config from DB
	var cfg struct {
		URL    string `db:"zenproxy_url"`
		APIKey string `db:"zenproxy_api_key"`
	}
	if err := z.db.Get(&cfg, "SELECT zenproxy_url, zenproxy_api_key FROM system_config WHERE id = 1"); err != nil {
		cfg.URL = defaultZenProxyBase
		cfg.APIKey = defaultZenProxyAPIKey
	}
	if cfg.URL == "" {
		cfg.URL = defaultZenProxyBase
	}
	if cfg.APIKey == "" {
		cfg.APIKey = defaultZenProxyAPIKey
	}

	var all []zenProxyItem
	for _, ptype := range []string{"socks", "http"} {
		u := fmt.Sprintf("%s/api/fetch?api_key=%s&count=50&type=%s", cfg.URL, cfg.APIKey, ptype)
		resp, err := http.Get(u)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			Proxies []zenProxyItem `json:"proxies"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		all = append(all, result.Proxies...)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no proxies returned")
	}
	return all, nil
}

func (z *ZenProxyService) buildProxyURL(p zenProxyItem) string {
	if p.Server == "" || p.Port == 0 {
		return ""
	}

	proxyType := strings.ToLower(p.Type)
	var scheme string
	switch {
	case strings.HasPrefix(proxyType, "socks5"):
		scheme = "socks5"
	case strings.HasPrefix(proxyType, "socks"):
		scheme = "socks5"
	case strings.HasPrefix(proxyType, "https"):
		scheme = "https"
	default:
		scheme = "http"
	}

	// Check for auth in quality
	var username, password string
	if p.Quality != nil {
		username, _ = p.Quality["username"].(string)
		password, _ = p.Quality["password"].(string)
	}

	if username != "" && password != "" {
		u := url.URL{
			Scheme: scheme,
			User:   url.UserPassword(username, password),
			Host:   fmt.Sprintf("%s:%d", p.Server, int(p.Port)),
		}
		return u.String()
	}
	return fmt.Sprintf("%s://%s:%d", scheme, p.Server, int(p.Port))
}

func (z *ZenProxyService) validateProxy(proxyURL string) bool {
	client, err := util.NewHTTPClient(proxyURL, 10*time.Second)
	if err != nil {
		return false
	}
	req, _ := http.NewRequest("GET", supabaseTestURL, nil)
	req.Header.Set("apikey", SupabaseAnonKey)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
