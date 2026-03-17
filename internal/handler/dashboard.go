package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/database"
	"github.com/jonasen/askcodi-go/internal/service"
	"github.com/jonasen/askcodi-go/internal/util"
)

type DashboardHandler struct {
	db         *sqlx.DB
	regSvc     *service.RegistrationService
	proxyMgr   *service.ProxyManager
	zenProxySvc *service.ZenProxyService
	logger     *service.Logger
}

func NewDashboardHandler(db *sqlx.DB, regSvc *service.RegistrationService, proxyMgr *service.ProxyManager, logger *service.Logger) *DashboardHandler {
	return &DashboardHandler{db: db, regSvc: regSvc, proxyMgr: proxyMgr, zenProxySvc: service.NewZenProxyService(db, logger), logger: logger}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

// GET /api/dashboard/stats
func (h *DashboardHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	var totalAccounts, activeAccounts, totalProxies int
	var totalTokens *int64

	h.db.Get(&totalAccounts, "SELECT COUNT(*) FROM accounts")
	h.db.Get(&activeAccounts, "SELECT COUNT(*) FROM accounts WHERE status = 'Active'")
	h.db.Get(&totalProxies, "SELECT COUNT(*) FROM proxies")
	h.db.Get(&totalTokens, "SELECT SUM(tokens_remaining) FROM accounts WHERE status = 'Active'")

	tokens := int64(0)
	if totalTokens != nil {
		tokens = *totalTokens
	}

	writeJSON(w, 200, map[string]interface{}{
		"total_accounts":  totalAccounts,
		"active_accounts": activeAccounts,
		"total_proxies":   totalProxies,
		"total_tokens":    tokens,
	})
}

// GET /api/accounts?page=1&page_size=20&status=Active
func (h *DashboardHandler) GetAccounts(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	statusFilter := r.URL.Query().Get("status")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with optional status filter
	where := ""
	args := []interface{}{}
	if statusFilter != "" {
		where = " WHERE status = ?"
		args = append(args, statusFilter)
	}

	// Total count
	var total int
	h.db.Get(&total, "SELECT COUNT(*) FROM accounts"+where, args...)

	// Paginated data
	query := "SELECT * FROM accounts" + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)
	var accounts []database.Account
	h.db.Select(&accounts, query, args...)

	items := make([]map[string]interface{}, len(accounts))
	for i, a := range accounts {
		items[i] = map[string]interface{}{
			"id":               a.ID,
			"email":            a.Email,
			"status":           a.Status,
			"tokens_remaining": a.TokensRemaining,
			"created_at":       a.CreatedAt,
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	writeJSON(w, 200, map[string]interface{}{
		"items":       items,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

// POST /api/accounts/register
func (h *DashboardHandler) TriggerRegistration(w http.ResponseWriter, r *http.Request) {
	var cfg database.SystemConfig
	batchSize, concurrency := 5, 2
	if err := h.db.Get(&cfg, "SELECT * FROM system_config WHERE id = 1"); err == nil {
		batchSize = cfg.BatchSize
		concurrency = cfg.Concurrency
	}

	go h.regSvc.RunBatchRegistration(batchSize, concurrency)
	writeJSON(w, 200, map[string]interface{}{
		"status": "Batch registration tasks started", "batch_size": batchSize, "concurrency": concurrency,
	})
}

// POST /api/accounts/{id}/refresh
func (h *DashboardHandler) RefreshAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "Invalid account ID")
		return
	}

	var account database.Account
	if err := h.db.Get(&account, "SELECT * FROM accounts WHERE id = ?", id); err != nil {
		writeError(w, 404, "Account not found")
		return
	}

	proxyURL, _ := h.proxyMgr.GetRandomProxy(nil)
	client, err := h.regSvc.GetClient(proxyURL)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"status": "failed", "error": err.Error()})
		return
	}

	balance, errMsg := h.regSvc.RefreshAndQueryBalance(client, &account)
	if errMsg != "" {
		writeJSON(w, 200, map[string]interface{}{"status": "failed", "error": errMsg})
		return
	}
	if balance == nil {
		writeJSON(w, 200, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("Balance API returned empty data. workspace_id=%s", account.WorkspaceID)})
		return
	}

	tokensRemaining := int64(0)
	hasTokens := false
	if tr, ok := balance["tokens_remaining"].(float64); ok {
		tokensRemaining = int64(tr)
		hasTokens = true
	}
	if !hasTokens {
		writeJSON(w, 200, map[string]interface{}{"status": "failed", "error": "Balance response missing tokens_remaining field"})
		return
	}
	status := "Active"
	if tokensRemaining <= 0 {
		status = "Exhausted"
	}
	h.db.Exec("UPDATE accounts SET tokens_remaining = ?, status = ?, access_token = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		tokensRemaining, status, account.AccessToken, id)

	writeJSON(w, 200, map[string]interface{}{"status": "success", "tokens": tokensRemaining})
}

// POST /api/accounts/refresh_all
func (h *DashboardHandler) RefreshAllTokens(w http.ResponseWriter, r *http.Request) {
	go func() {
		var accountIDs []int64
		h.db.Select(&accountIDs, "SELECT id FROM accounts")

		for _, accID := range accountIDs {
			var acc database.Account
			if err := h.db.Get(&acc, "SELECT * FROM accounts WHERE id = ?", accID); err != nil {
				continue
			}
			proxyURL, _ := h.proxyMgr.GetRandomProxy(nil)
			client, err := util.NewHTTPClient(proxyURL, 30*time.Second)
			if err != nil {
				continue
			}

			balance, errMsg := h.regSvc.RefreshAndQueryBalance(client, &acc)
			if errMsg != "" {
				fmt.Printf("[RefreshAll] %s: %s\n", acc.Email, errMsg)
			}
			if balance != nil {
				if tr, ok := balance["tokens_remaining"].(float64); ok {
					tokens := int64(tr)
					status := "Active"
					if tokens <= 0 {
						status = "Exhausted"
					}
					h.db.Exec("UPDATE accounts SET tokens_remaining = ?, status = ?, access_token = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
						tokens, status, acc.AccessToken, accID)
				}
			}
			time.Sleep(2 * time.Second) // Delay between accounts
		}
	}()
	writeJSON(w, 200, map[string]string{"status": "Bulk refresh task started"})
}

// POST /api/accounts/{id}/disable
func (h *DashboardHandler) DisableAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "Invalid account ID")
		return
	}
	result, _ := h.db.Exec("UPDATE accounts SET status = 'Disabled (Manual)', updated_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	if rows, _ := result.RowsAffected(); rows == 0 {
		writeError(w, 404, "Account not found")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "success"})
}

// DELETE /api/accounts/{id}
func (h *DashboardHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "Invalid account ID")
		return
	}
	result, _ := h.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if rows, _ := result.RowsAffected(); rows == 0 {
		writeError(w, 404, "Account not found")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "success"})
}

// GET /api/proxies
func (h *DashboardHandler) GetProxies(w http.ResponseWriter, r *http.Request) {
	var proxies []struct {
		ID        int64  `db:"id"`
		URL       string `db:"url"`
		Status    string `db:"status"`
		FailCount int    `db:"fail_count"`
	}
	if err := h.db.Select(&proxies, "SELECT id, url, status, fail_count FROM proxies ORDER BY id DESC"); err != nil {
		writeError(w, 500, "Failed to load proxies: "+err.Error())
		return
	}

	result := make([]map[string]interface{}, len(proxies))
	for i, p := range proxies {
		result[i] = map[string]interface{}{
			"id": p.ID, "url": p.URL, "status": p.Status, "fail_count": p.FailCount,
		}
	}
	writeJSON(w, 200, result)
}

// POST /api/proxies
func (h *DashboardHandler) AddProxy(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		URL string `json:"url"`
	}
	json.Unmarshal(body, &req)
	if req.URL == "" {
		writeError(w, 400, "URL is required")
		return
	}

	// Check duplicate
	var count int
	h.db.Get(&count, "SELECT COUNT(*) FROM proxies WHERE url = ?", req.URL)
	if count > 0 {
		writeError(w, 400, "Proxy already exists")
		return
	}

	result, err := h.db.Exec("INSERT INTO proxies (url, status, fail_count) VALUES (?, 'Active', 0)", req.URL)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	id, _ := result.LastInsertId()
	writeJSON(w, 200, map[string]interface{}{"status": "success", "id": id})
}

// DELETE /api/proxies/{id}
func (h *DashboardHandler) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "Invalid proxy ID")
		return
	}
	result, _ := h.db.Exec("DELETE FROM proxies WHERE id = ?", id)
	if rows, _ := result.RowsAffected(); rows == 0 {
		writeError(w, 404, "Proxy not found")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "success"})
}

// GET /api/config
func (h *DashboardHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	var cfg database.SystemConfig
	if err := h.db.Get(&cfg, "SELECT * FROM system_config WHERE id = 1"); err != nil {
		writeError(w, 404, "Config not found")
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"batch_size":                   cfg.BatchSize,
		"concurrency":                  cfg.Concurrency,
		"auto_register_interval_minutes": cfg.AutoRegisterIntervalMinutes,
		"min_account_threshold":        cfg.MinAccountThreshold,
		"gptmail_api_key":              cfg.GptmailAPIKey,
		"proxy_enabled":                cfg.ProxyEnabled,
		"zenproxy_url":                 cfg.ZenProxyURL,
		"zenproxy_api_key":             cfg.ZenProxyAPIKey,
	})
}

// PUT /api/config
func (h *DashboardHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		BatchSize                   int    `json:"batch_size"`
		Concurrency                 int    `json:"concurrency"`
		AutoRegisterIntervalMinutes int    `json:"auto_register_interval_minutes"`
		MinAccountThreshold         int    `json:"min_account_threshold"`
		GptmailAPIKey               string `json:"gptmail_api_key"`
		ProxyEnabled                bool   `json:"proxy_enabled"`
		ZenProxyURL                 string `json:"zenproxy_url"`
		ZenProxyAPIKey              string `json:"zenproxy_api_key"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "Invalid JSON")
		return
	}

	_, err := h.db.Exec(
		`UPDATE system_config SET batch_size=?, concurrency=?, auto_register_interval_minutes=?,
		 min_account_threshold=?, gptmail_api_key=?, proxy_enabled=?,
		 zenproxy_url=?, zenproxy_api_key=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`,
		req.BatchSize, req.Concurrency, req.AutoRegisterIntervalMinutes,
		req.MinAccountThreshold, req.GptmailAPIKey, req.ProxyEnabled,
		req.ZenProxyURL, req.ZenProxyAPIKey,
	)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "success"})
}

// POST /api/proxies/refresh-zenproxy
func (h *DashboardHandler) RefreshZenProxies(w http.ResponseWriter, r *http.Request) {
	go h.zenProxySvc.FetchAndRefreshProxies()
	writeJSON(w, 200, map[string]string{"status": "started"})
}

func (h *DashboardHandler) GetRegistrationLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.logger.GetLogs())
}
