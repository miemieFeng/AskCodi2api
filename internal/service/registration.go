package service

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/database"
	"github.com/jonasen/askcodi-go/internal/util"
)

const (
	GptmailAPI      = "https://mail-gateway-api.jdyang.workers.dev"  // Cloudflare Email Worker
	CFMailDomain    = "miemie.hk"
	SupabaseURL     = "https://umnszlghpeqeuclzpjoy.supabase.co"
	AskCodiURL      = "https://www.askcodi.com"
	SupabaseAnonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InVtbnN6bGdocGVxZXVjbHpwam95Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3NTI2ODM0MDMsImV4cCI6MjA2ODI1OTQwM30.YRR7YquYqzZoUbqviTAJA5cinR0rI1pDNeItcmZjP6E"
	MaxProxyRetries = 3
	UserAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
)

type RegistrationService struct {
	db       *sqlx.DB
	proxyMgr *ProxyManager
	logger   *Logger
}

func NewRegistrationService(db *sqlx.DB, proxyMgr *ProxyManager, logger *Logger) *RegistrationService {
	return &RegistrationService{db: db, proxyMgr: proxyMgr, logger: logger}
}

func baseHeaders() map[string]string {
	return map[string]string{
		"authorization":           "Bearer " + SupabaseAnonKey,
		"apikey":                  SupabaseAnonKey,
		"x-supabase-api-version": "2024-01-01",
		"x-application-name":     "askcodi-app",
		"x-client-info":          "supabase-js-web/2.52.0",
		"origin":                 "https://www.askcodi.com",
		"referer":                "https://www.askcodi.com/",
		"user-agent":             UserAgent,
		"accept-language":        "zh-CN,zh;q=0.9",
	}
}

func objHeaders(accessToken string) map[string]string {
	h := baseHeaders()
	h["authorization"] = "Bearer " + accessToken
	h["content-profile"] = "public"
	h["content-type"] = "application/json"
	h["accept"] = "application/vnd.pgrst.object+json"
	h["prefer"] = "return=representation"
	return h
}

func minHeaders(accessToken string) map[string]string {
	h := baseHeaders()
	h["authorization"] = "Bearer " + accessToken
	h["content-profile"] = "public"
	h["content-type"] = "application/json"
	h["prefer"] = "return=minimal"
	return h
}

func setHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

func doJSON(client *http.Client, method, url string, headers map[string]string, body interface{}) (map[string]interface{}, int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	setHeaders(req, headers)

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var result map[string]interface{}
	json.Unmarshal(data, &result) // may fail for non-JSON responses, that's ok
	return result, resp.StatusCode, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Registration Steps ---

func (rs *RegistrationService) generateEmail(client *http.Client, apiKey string) (string, error) {
	// Generate random email using our own domain (Cloudflare Email Worker)
	randBytes := make([]byte, 8)
	rand.Read(randBytes)
	prefix := fmt.Sprintf("askcodi-%x", randBytes)
	email := prefix + "@" + CFMailDomain
	return email, nil
}

func (rs *RegistrationService) signup(client *http.Client, email, password, codeChallenge string) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"email":    email,
		"password": password,
		"data": map[string]interface{}{
			"marketing":    true,
			"utm_source":   nil,
			"utm_campaign": nil,
			"utm_medium":   nil,
			"utm_term":     nil,
			"utm_content":  nil,
		},
		"gotrue_meta_security":  map[string]interface{}{},
		"code_challenge":        codeChallenge,
		"code_challenge_method": "s256",
	}
	headers := baseHeaders()
	headers["content-type"] = "application/json;charset=UTF-8"

	redirectTo := "https://www.askcodi.com/auth?type=confirm"
	signupURL := SupabaseURL + "/auth/v1/signup?redirect_to=" + url.QueryEscape(redirectTo)
	result, _, err := doJSON(client, "POST", signupURL, headers, payload)
	return result, err
}

func (rs *RegistrationService) waitForConfirmEmail(client *http.Client, email, apiKey string) (string, error) {
	urlRe := regexp.MustCompile(`https?://[^\s"<>\]]+`)

	for i := 0; i < 18; i++ { // 90s / 5s = 18 iterations
		time.Sleep(5 * time.Second)
		// Use Cloudflare Email Worker API
		reqURL := GptmailAPI + "/messages?address=" + url.QueryEscape(email)
		req, _ := http.NewRequest("GET", reqURL, nil)
		req.Header.Set("Authorization", "Bearer " + apiKey)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		var data map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			continue
		}
		messages, _ := data["messages"].([]interface{})
		if len(messages) == 0 {
			continue
		}
		emailObj, _ := messages[0].(map[string]interface{})
		body := ""
		if t, ok := emailObj["text"].(string); ok && t != "" {
			body = t
		} else if h, ok := emailObj["html"].(string); ok {
			body = strings.ReplaceAll(h, "&amp;", "&")
		}
		urls := urlRe.FindAllString(body, -1)
		for _, u := range urls {
			if strings.Contains(u, "verify") || strings.Contains(u, "confirm") {
				return u, nil
			}
		}
	}
	return "", nil
}

func (rs *RegistrationService) resendConfirmation(client *http.Client, email string) error {
	headers := baseHeaders()
	headers["content-type"] = "application/json;charset=UTF-8"
	_, _, err := doJSON(client, "POST", SupabaseURL+"/auth/v1/resend", headers, map[string]interface{}{
		"type": "signup", "email": email,
	})
	return err
}

func (rs *RegistrationService) confirmAndGetCode(proxyURL, confirmURL string) (string, error) {
	client, err := util.NewHTTPClient(proxyURL, 30*time.Second)
	if err != nil {
		return "", err
	}
	// Follow redirects and capture the final URL
	var finalURL string
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		finalURL = req.URL.String()
		return nil
	}
	resp, err := client.Get(confirmURL)
	if err != nil {
		// If redirect was captured before error, try using it
		if finalURL != "" {
			return extractCode(finalURL), nil
		}
		return "", err
	}
	defer resp.Body.Close()

	if finalURL == "" {
		finalURL = resp.Request.URL.String()
	}
	return extractCode(finalURL), nil
}

func extractCode(url string) string {
	re := regexp.MustCompile(`[?&#]code=([^&#]+)`)
	m := re.FindStringSubmatch(url)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func (rs *RegistrationService) loginPKCE(client *http.Client, authCode, codeVerifier string) (string, string, error) {
	headers := baseHeaders()
	headers["content-type"] = "application/json;charset=UTF-8"
	result, _, err := doJSON(client, "POST", SupabaseURL+"/auth/v1/token?grant_type=pkce", headers, map[string]interface{}{
		"auth_code": authCode, "code_verifier": codeVerifier,
	})
	if err != nil {
		return "", "", err
	}
	token, _ := result["access_token"].(string)
	user, _ := result["user"].(map[string]interface{})
	uid, _ := user["id"].(string)
	return token, uid, nil
}

func (rs *RegistrationService) loginPassword(client *http.Client, email, password string) (string, string, error) {
	headers := baseHeaders()
	headers["content-type"] = "application/json;charset=UTF-8"
	result, status, err := doJSON(client, "POST", SupabaseURL+"/auth/v1/token?grant_type=password", headers, map[string]interface{}{
		"email": email, "password": password, "gotrue_meta_security": map[string]interface{}{},
	})
	if err != nil {
		return "", "", err
	}
	if _, ok := result["access_token"]; !ok {
		errMsg := fmt.Sprintf("Login failed (%d)", status)
		if d, ok := result["error_description"].(string); ok {
			errMsg += ": " + d
		} else if e, ok := result["error"].(string); ok {
			errMsg += ": " + e
		}
		return "", "", fmt.Errorf("%s", errMsg)
	}
	token, _ := result["access_token"].(string)
	user, _ := result["user"].(map[string]interface{})
	uid, _ := user["id"].(string)
	return token, uid, nil
}

func (rs *RegistrationService) createProfile(client *http.Client, email, userID, accessToken string) error {
	_, _, err := doJSON(client, "POST", SupabaseURL+"/rest/v1/profiles?select=*", objHeaders(accessToken), map[string]interface{}{
		"id": userID, "email": email,
	})
	return err
}

func (rs *RegistrationService) createWorkspace(client *http.Client, userID, accessToken string) (string, error) {
	slug := "ws-" + randomHex(6)
	result, _, err := doJSON(client, "POST", SupabaseURL+"/rest/v1/workspaces?select=*", objHeaders(accessToken), map[string]interface{}{
		"name": "My Workspace", "owner_id": userID, "slug": slug,
	})
	if err != nil {
		return "", err
	}
	id, _ := result["id"].(string)
	return id, nil
}

func (rs *RegistrationService) addWorkspaceMember(client *http.Client, workspaceID, userID, accessToken string) error {
	_, _, err := doJSON(client, "POST", SupabaseURL+"/rest/v1/workspace_members", minHeaders(accessToken), map[string]interface{}{
		"workspace_id": workspaceID, "user_id": userID, "role": "owner",
	})
	return err
}

func (rs *RegistrationService) createProject(client *http.Client, workspaceID, userID, accessToken string) (string, error) {
	slug := "proj-" + randomHex(6)
	result, _, err := doJSON(client, "POST", SupabaseURL+"/rest/v1/projects?select=*", objHeaders(accessToken), map[string]interface{}{
		"workspace_id": workspaceID, "name": "Default Project", "slug": slug, "created_by": userID,
	})
	if err != nil {
		return "", err
	}
	id, _ := result["id"].(string)
	return id, nil
}

func (rs *RegistrationService) createTrial(client *http.Client, workspaceID, accessToken string) error {
	headers := map[string]string{
		"authorization": "Bearer " + accessToken,
		"content-type":  "application/json",
		"user-agent":    UserAgent,
		"origin":        "https://www.askcodi.com",
		"referer":       "https://www.askcodi.com/setup/workspace",
	}
	_, status, err := doJSON(client, "POST", AskCodiURL+"/api/subscription/create-trial-plan", headers, map[string]interface{}{
		"workspaceId": workspaceID,
	})
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("create trial failed with status %d", status)
	}
	return nil
}

func (rs *RegistrationService) queryBalance(client *http.Client, workspaceID, accessToken string) (map[string]interface{}, error) {
	headers := baseHeaders()
	headers["authorization"] = "Bearer " + accessToken
	headers["accept"] = "application/json"

	req, _ := http.NewRequest("GET", SupabaseURL+"/rest/v1/workspace_subscriptions?select=*&workspace_id=eq."+workspaceID, nil)
	setHeaders(req, headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		// Check if it's an error object
		var obj map[string]interface{}
		if json.Unmarshal(body, &obj) == nil {
			if code, _ := obj["code"].(string); code == "PGRST301" {
				return map[string]interface{}{"error": "JWT expired"}, nil
			}
		}
		return nil, nil
	}
	if len(arr) > 0 {
		return arr[0], nil
	}
	return nil, nil
}

func (rs *RegistrationService) createAPIKey(client *http.Client, workspaceID, projectID, userID, accessToken string) (string, error) {
	rawToken := randomHex(32)
	encryptedToken := "ak-" + rawToken
	h := sha256.Sum256([]byte(encryptedToken))
	tokenHash := hex.EncodeToString(h[:])

	result, _, err := doJSON(client, "POST", SupabaseURL+"/rest/v1/workspace_api_tokens?select=*", objHeaders(accessToken), map[string]interface{}{
		"workspace_id":    workspaceID,
		"project_id":      projectID,
		"user_id":         userID,
		"name":            "Default API Key",
		"token_hash":      tokenHash,
		"encrypted_token": encryptedToken,
		"permissions":     map[string]interface{}{},
		"is_active":       true,
	})
	if err != nil {
		return "", err
	}
	token, _ := result["encrypted_token"].(string)
	return token, nil
}

func (rs *RegistrationService) RefreshAndQueryBalance(client *http.Client, account *database.Account) (map[string]interface{}, string) {
	accessToken, _, err := rs.loginPassword(client, account.Email, account.Password)
	if err != nil {
		return nil, fmt.Sprintf("Re-login failed: %v", err)
	}
	// Update access token in memory
	account.AccessToken = accessToken

	balance, err := rs.queryBalance(client, account.WorkspaceID, accessToken)
	if err != nil {
		return nil, fmt.Sprintf("Query balance failed: %v", err)
	}
	if balance != nil {
		if errMsg, ok := balance["error"].(string); ok {
			return nil, errMsg
		}
	}
	return balance, ""
}

// GetClient creates an HTTP client with optional proxy.
func (rs *RegistrationService) GetClient(proxyURL string) (*http.Client, error) {
	return util.NewHTTPClient(proxyURL, 30*time.Second)
}

// RunRegistrationFlow executes the full registration pipeline.
func (rs *RegistrationService) RunRegistrationFlow() map[string]interface{} {
	triedProxies := make(map[string]bool)

	for attempt := 0; attempt < MaxProxyRetries; attempt++ {
		proxyURL, _ := rs.proxyMgr.GetRandomProxy(mapKeys(triedProxies))

		// Get gptmail API key from config
		var cfg database.SystemConfig
		gptmailKey := "gpt-test"
		if err := rs.db.Get(&cfg, "SELECT * FROM system_config WHERE id = 1"); err == nil {
			gptmailKey = cfg.GptmailAPIKey
		}

		proxyLog := proxyURL
		if proxyLog == "" {
			proxyLog = "Direct connection"
		}
		rs.logger.Log(fmt.Sprintf("Starting registration flow... [%s] (attempt %d/%d)", proxyLog, attempt+1, MaxProxyRetries))

		// GPTMail: always direct (no proxy needed for mail service)
		mailClient, _ := util.NewHTTPClient("", 30*time.Second)
		// Supabase/AskCodi: use proxy
		client, err := util.NewHTTPClient(proxyURL, 30*time.Second)
		if err != nil {
			rs.logger.Log(fmt.Sprintf("Failed to create HTTP client: %v", err))
			continue
		}

		result := rs.doRegistration(mailClient, client, proxyURL, gptmailKey)

		if result["status"] == "proxy_failed" {
			if proxyURL != "" {
				triedProxies[proxyURL] = true
				rs.proxyMgr.MarkProxyFailed(proxyURL)
				rs.logger.Log(fmt.Sprintf("Proxy %s failed (%s), retrying with another proxy...", proxyURL, result["error"]))
			} else {
				rs.logger.Log(fmt.Sprintf("Direct connection failed (%s), no proxy available to retry.", result["error"]))
				return map[string]interface{}{"status": "failed", "error": result["error"]}
			}
			continue
		}

		return result
	}

	rs.logger.Log(fmt.Sprintf("All %d proxy retries exhausted.", MaxProxyRetries))
	return map[string]interface{}{"status": "failed", "error": "All proxy retries exhausted"}
}

func (rs *RegistrationService) doRegistration(mailClient, client *http.Client, proxyURL, gptmailKey string) map[string]interface{} {
	email, err := rs.generateEmail(mailClient, gptmailKey)
	if err != nil {
		return map[string]interface{}{"status": "failed", "error": err.Error()}
	}
	password := util.GeneratePassword(12)
	rs.logger.Log(fmt.Sprintf("Generated temp email: %s", email))

	codeVerifier := util.GenerateCodeVerifier()
	codeChallenge := util.GenerateCodeChallenge(codeVerifier)

	if _, err := rs.signup(client, email, password, codeChallenge); err != nil {
		return rs.classifyError(err, proxyURL)
	}
	rs.logger.Log(fmt.Sprintf("[%s] Signup request sent. Waiting for confirmation link...", email))

	var confirmURL string
	for emailAttempt := 0; emailAttempt < 3; emailAttempt++ {
		if emailAttempt > 0 {
			rs.logger.Log(fmt.Sprintf("[%s] Confirmation email not received, resending (%d/3)...", email, emailAttempt+1))
			rs.resendConfirmation(client, email)
		}
		confirmURL, _ = rs.waitForConfirmEmail(mailClient, email, gptmailKey)
		if confirmURL != "" {
			break
		}
	}
	if confirmURL == "" {
		return map[string]interface{}{"status": "failed", "error": "Did not receive confirmation email after 3 attempts"}
	}

	rs.logger.Log(fmt.Sprintf("[%s] Received confirmation link. Authorizing...", email))

	authCode, _ := rs.confirmAndGetCode(proxyURL, confirmURL)
	var accessToken, userID string
	if authCode != "" {
		accessToken, userID, err = rs.loginPKCE(client, authCode, codeVerifier)
	} else {
		accessToken, userID, err = rs.loginPassword(client, email, password)
	}
	if err != nil {
		return rs.classifyError(err, proxyURL)
	}

	rs.logger.Log(fmt.Sprintf("[%s] Setting up User Workspace & Trial API Key...", email))

	rs.createProfile(client, email, userID, accessToken)
	workspaceID, err := rs.createWorkspace(client, userID, accessToken)
	if err != nil {
		return rs.classifyError(err, proxyURL)
	}
	rs.addWorkspaceMember(client, workspaceID, userID, accessToken)
	projectID, err := rs.createProject(client, workspaceID, userID, accessToken)
	if err != nil {
		return rs.classifyError(err, proxyURL)
	}

	if err := rs.createTrial(client, workspaceID, accessToken); err != nil {
		rs.logger.Log(fmt.Sprintf("[%s] Warning: create trial failed: %v", email, err))
	}

	// Wait for trial to be provisioned before querying balance
	time.Sleep(2 * time.Second)

	apiKey, _ := rs.createAPIKey(client, workspaceID, projectID, userID, accessToken)

	balance, _ := rs.queryBalance(client, workspaceID, accessToken)
	var tokensRemaining int64
	if balance != nil {
		if tr, ok := balance["tokens_remaining"].(float64); ok {
			tokensRemaining = int64(tr)
		}
	}

	// Save to database
	_, err = rs.db.Exec(
		`INSERT INTO accounts (email, password, access_token, workspace_id, project_id, user_id, api_key, status, tokens_remaining)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'Active', ?)`,
		email, password, accessToken, workspaceID, projectID, userID, apiKey, tokensRemaining,
	)
	if err != nil {
		rs.logger.Log(fmt.Sprintf("[%s] Database save failed: %v", email, err))
		return map[string]interface{}{"status": "failed", "error": err.Error()}
	}

	rs.logger.Log(fmt.Sprintf("[%s] Successfully completed registration! Token added to pool.", email))
	return map[string]interface{}{"status": "success", "email": email}
}

func (rs *RegistrationService) classifyError(err error, proxyURL string) map[string]interface{} {
	errMsg := err.Error()
	// Check if it's a connection/proxy error
	if isConnectionError(errMsg) && proxyURL != "" {
		return map[string]interface{}{"status": "proxy_failed", "error": errMsg}
	}
	rs.logger.Log(fmt.Sprintf("Registration Error: %s", errMsg))
	if proxyURL != "" {
		rs.proxyMgr.MarkProxyFailed(proxyURL)
	}
	return map[string]interface{}{"status": "failed", "error": errMsg}
}

func isConnectionError(msg string) bool {
	lower := strings.ToLower(msg)
	keywords := []string{"connect", "timeout", "refused", "reset", "eof", "dial"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// RunBatchRegistration runs multiple registrations with concurrency control.
func (rs *RegistrationService) RunBatchRegistration(batchSize, concurrency int) {
	rs.logger.Log(fmt.Sprintf("--- Triggered Batch Registration: Size %d, Concurrency %d ---", batchSize, concurrency))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < batchSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rs.RunRegistrationFlow()
		}()
	}
	wg.Wait()
	rs.logger.Log(fmt.Sprintf("--- Batch Registration Finished: Sent %d tasks ---", batchSize))
}
