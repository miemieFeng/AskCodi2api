package service

import (
	"math/rand"
	"time"

	"github.com/jmoiron/sqlx"
)

type ProxyManager struct {
	db *sqlx.DB
}

func NewProxyManager(db *sqlx.DB) *ProxyManager {
	return &ProxyManager{db: db}
}

func (pm *ProxyManager) IsProxyEnabled() (bool, error) {
	var enabled bool
	if err := pm.db.Get(&enabled, "SELECT proxy_enabled FROM system_config WHERE id = 1"); err != nil {
		return true, err // default to enabled on error
	}
	return enabled, nil
}

func (pm *ProxyManager) GetRandomProxy(excludeURLs []string) (string, error) {
	enabled, err := pm.IsProxyEnabled()
	if err != nil {
		return "", nil // direct connection on error
	}
	if !enabled {
		return "", nil // proxy disabled, direct connection
	}

	var urls []string
	query := "SELECT url FROM proxies WHERE status = 'Active'"
	args := []interface{}{}

	if len(excludeURLs) > 0 {
		placeholders := ""
		for i, u := range excludeURLs {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, u)
		}
		query += " AND url NOT IN (" + placeholders + ")"
	}

	if err := pm.db.Select(&urls, query, args...); err != nil {
		return "", err
	}
	if len(urls) == 0 {
		return "", nil
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return urls[r.Intn(len(urls))], nil
}

func (pm *ProxyManager) MarkProxyFailed(proxyURL string) error {
	if proxyURL == "" {
		return nil
	}
	_, err := pm.db.Exec(
		"UPDATE proxies SET fail_count = fail_count + 1, status = CASE WHEN fail_count + 1 >= 5 THEN 'Failed' ELSE status END, updated_at = CURRENT_TIMESTAMP WHERE url = ?",
		proxyURL,
	)
	return err
}

func (pm *ProxyManager) ResetProxy(proxyURL string) error {
	_, err := pm.db.Exec(
		"UPDATE proxies SET fail_count = 0, status = 'Active', updated_at = CURRENT_TIMESTAMP WHERE url = ?",
		proxyURL,
	)
	return err
}
