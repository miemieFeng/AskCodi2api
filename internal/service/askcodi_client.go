package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/util"
)

const (
	AskCodiAPIBase    = "https://api.askcodi.com"
	MaxRetryAccounts  = 5
)

type AskCodiClient struct {
	db       *sqlx.DB
	acctMgr  *AccountManager
	proxyMgr *ProxyManager

	// Model cache
	modelCacheMu   sync.RWMutex
	modelCache     interface{}
	modelCacheTime time.Time
}

func NewAskCodiClient(db *sqlx.DB, acctMgr *AccountManager, proxyMgr *ProxyManager) *AskCodiClient {
	return &AskCodiClient{db: db, acctMgr: acctMgr, proxyMgr: proxyMgr}
}

// Model mapping: old Claude model names -> new names
var modelMapping = map[string]string{
	"claude-3-7-sonnet-20250219":    "anthropic/claude-sonnet-4-6",
	"claude-3-5-sonnet-20241022":    "anthropic/claude-sonnet-4-5",
	"claude-3-5-sonnet-20240620":    "anthropic/claude-sonnet-4-5",
	"claude-3-5-haiku-20241022":     "anthropic/claude-haiku-4-5",
	"claude-3-opus-20240229":        "anthropic/claude-opus-4-6",
	"claude-3-sonnet-20240229":      "anthropic/claude-sonnet-4-5",
	"claude-3-haiku-20240307":       "anthropic/claude-haiku-4-5",
	"anthropic/claude-sonnet-4.6":   "anthropic/claude-sonnet-4-6",
}

var standardAliases = []string{
	"claude-haiku-4-5", "claude-haiku-4-5-20251001",
	"claude-opus-4-6", "claude-sonnet-4-6",
	"claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20240620",
	"claude-3-5-haiku-20241022", "claude-3-opus-20240229", "claude-3-sonnet-20240229",
	"claude-3-haiku-20240307",
}

const modelCacheTTL = 5 * time.Minute

func buildModelData(ids []string) []map[string]interface{} {
	data := make([]map[string]interface{}, len(ids))
	for i, id := range ids {
		data[i] = map[string]interface{}{
			"id": id, "object": "model", "created": 1686935002, "owned_by": "askcodi",
		}
	}
	return data
}

// GetModels returns models from upstream AskCodi API with caching, plus standard aliases.
func (c *AskCodiClient) GetModels() (interface{}, error) {
	// Check cache
	c.modelCacheMu.RLock()
	if c.modelCache != nil && time.Since(c.modelCacheTime) < modelCacheTTL {
		cached := c.modelCache
		c.modelCacheMu.RUnlock()
		return cached, nil
	}
	c.modelCacheMu.RUnlock()

	aliasData := buildModelData(standardAliases)

	account, err := c.acctMgr.GetActiveAccount(nil)
	if err != nil || account == nil {
		// No account available, return aliases only
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}

	proxyURL, _ := c.proxyMgr.GetRandomProxy(nil)
	client, err := util.NewHTTPClient(proxyURL, 10*time.Second)
	if err != nil {
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}

	req, _ := http.NewRequest("GET", AskCodiAPIBase+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+account.APIKey)
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}

	var upstreamResult map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&upstreamResult); err != nil {
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}

	data, ok := upstreamResult["data"].([]interface{})
	if !ok {
		result := map[string]interface{}{"object": "list", "data": aliasData}
		return result, nil
	}

	// Append aliases
	for _, a := range aliasData {
		data = append(data, a)
	}
	upstreamResult["data"] = data

	// Update cache
	c.modelCacheMu.Lock()
	c.modelCache = upstreamResult
	c.modelCacheTime = time.Now()
	c.modelCacheMu.Unlock()

	return upstreamResult, nil
}

func applyModelMapping(model string) string {
	if mapped, ok := modelMapping[model]; ok {
		return mapped
	}
	if !strings.Contains(model, "/") {
		prefixes := map[string]string{
			"claude-":   "anthropic/",
			"gemini-":   "google/",
			"gpt-":     "openai/",
			"deepseek-": "deepseek/",
			"grok-":    "xai/",
			"minimax-": "minimax/",
			"kimi-":    "kimi/",
			"glm-":     "zai/",
			"trinity-": "arcee/",
		}
		for prefix, provider := range prefixes {
			if strings.HasPrefix(model, prefix) {
				return provider + model
			}
		}
	}
	return model
}

func isTokenExhaustedError(statusCode int, body string) bool {
	if statusCode == 429 || statusCode == 402 {
		return true
	}
	return false
}

// isRequestTooLargeError returns true when the request itself exceeds per-account limits
// but the account is not actually exhausted.
func isRequestTooLargeError(statusCode int, body string) bool {
	if statusCode == 403 {
		lower := strings.ToLower(body)
		keywords := []string{"insufficient tokens", "estimated cost", "upgrade your plan", "limit reached"}
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}

var errRetryNextAccount = fmt.Errorf("retry with next account")

// ChatCompletionsHandler handles OpenAI-compatible chat completions.
func (c *AskCodiClient) ChatCompletionsHandler(payload map[string]interface{}, w http.ResponseWriter) error {
	model, _ := payload["model"].(string)
	payload["model"] = applyModelMapping(model)
	isStream, _ := payload["stream"].(bool)

	triedIDs := make([]int64, 0)
	for attempt := 0; attempt < MaxRetryAccounts; attempt++ {
		account, err := c.acctMgr.GetActiveAccount(triedIDs)
		if err != nil || account == nil {
			http.Error(w, `{"error":"No active accounts available in the pool"}`, http.StatusServiceUnavailable)
			return nil
		}
		triedIDs = append(triedIDs, account.ID)
		proxyURL, _ := c.proxyMgr.GetRandomProxy(nil)

		var retErr error
		if isStream {
			retErr = c.doStreamChat(payload, account.APIKey, account.ID, account.Email, proxyURL, w, attempt)
		} else {
			retErr = c.doSyncChat(payload, account.APIKey, account.ID, account.Email, proxyURL, w, attempt)
		}
		if retErr == errRetryNextAccount {
			continue
		}
		return retErr
	}

	http.Error(w, `{"error":"All active accounts exhausted. Please register new accounts."}`, http.StatusServiceUnavailable)
	return nil
}

func (c *AskCodiClient) doStreamChat(payload map[string]interface{}, apiKey string, accountID int64, accountEmail string, proxyURL string, w http.ResponseWriter, attempt int) error {
	client, err := util.NewHTTPClient(proxyURL, 60*time.Second)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", AskCodiAPIBase+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		if proxyURL != "" {
			c.proxyMgr.MarkProxyFailed(proxyURL)
		}
		fmt.Printf("[API-Stream] Connection error: %v - proxy: %s\n", err, proxyURL)
		return errRetryNextAccount
	}

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		errText := string(errBody)

		if isRequestTooLargeError(resp.StatusCode, errText) {
			fmt.Printf("[API-Stream] Request too large for account %s, returning error to client\n", accountEmail)
			http.Error(w, errText, resp.StatusCode)
			return nil
		}
		if isTokenExhaustedError(resp.StatusCode, errText) {
			c.acctMgr.DisableAccount(accountID, "Exhausted")
			fmt.Printf("[API-Stream] Account %s exhausted (attempt %d/%d), trying next...\n", accountEmail, attempt+1, MaxRetryAccounts)
			return errRetryNextAccount
		}
		if resp.StatusCode == 401 {
			c.acctMgr.DisableAccount(accountID, "Exhausted")
			fmt.Printf("[API-Stream] Account %s api_key invalid (401), marked Exhausted, trying next...\n", accountEmail)
			return errRetryNextAccount
		}
		http.Error(w, errText, resp.StatusCode)
		return nil
	}

	// Stream the response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	flusher, ok := w.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		if ok {
			flusher.Flush()
		}
	}
	resp.Body.Close()
	return nil
}

func (c *AskCodiClient) doSyncChat(payload map[string]interface{}, apiKey string, accountID int64, accountEmail string, proxyURL string, w http.ResponseWriter, attempt int) error {
	client, err := util.NewHTTPClient(proxyURL, 60*time.Second)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", AskCodiAPIBase+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		if proxyURL != "" {
			c.proxyMgr.MarkProxyFailed(proxyURL)
		}
		http.Error(w, fmt.Sprintf(`{"error":"Upstream connection error: %v"}`, err), http.StatusBadGateway)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		errText := string(errBody)

		fmt.Printf("[API] Account %s got HTTP %d: %s\n", accountEmail, resp.StatusCode, errText)

		if isRequestTooLargeError(resp.StatusCode, errText) {
			fmt.Printf("[API] Request too large for account %s, returning error to client\n", accountEmail)
			http.Error(w, errText, resp.StatusCode)
			return nil
		}
		if isTokenExhaustedError(resp.StatusCode, errText) {
			c.acctMgr.DisableAccount(accountID, "Exhausted")
			fmt.Printf("[API] Account %s exhausted (attempt %d/%d), trying next...\n", accountEmail, attempt+1, MaxRetryAccounts)
			return errRetryNextAccount
		}
		if resp.StatusCode == 401 {
			c.acctMgr.DisableAccount(accountID, "Exhausted")
			fmt.Printf("[API] Account %s api_key invalid (401), marked Exhausted, trying next...\n", accountEmail)
			return errRetryNextAccount
		}
		http.Error(w, errText, resp.StatusCode)
		return nil
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	io.Copy(w, resp.Body)
	return nil
}

// --- Anthropic Messages Support ---

// AnthropicMessages handles Anthropic-compatible /v1/messages requests.
func (c *AskCodiClient) AnthropicMessages(payload map[string]interface{}, w http.ResponseWriter) error {
	origModel, _ := payload["model"].(string)
	wantStream, _ := payload["stream"].(bool)
	openaiPayload := translateAnthropicToOpenAI(payload)
	model, _ := openaiPayload["model"].(string)
	openaiPayload["model"] = applyModelMapping(model)

	if wantStream {
		openaiPayload["stream"] = true
		return c.doStreamAnthropicMessages(openaiPayload, origModel, w)
	}

	// Non-streaming path
	openaiPayload["stream"] = false
	triedIDs := make([]int64, 0)
	for attempt := 0; attempt < MaxRetryAccounts; attempt++ {
		account, err := c.acctMgr.GetActiveAccount(triedIDs)
		if err != nil || account == nil {
			http.Error(w, `{"error":"No active accounts available"}`, http.StatusServiceUnavailable)
			return nil
		}
		triedIDs = append(triedIDs, account.ID)
		proxyURL, _ := c.proxyMgr.GetRandomProxy(nil)

		client, err := util.NewHTTPClient(proxyURL, 120*time.Second)
		if err != nil {
			continue
		}

		body, _ := json.Marshal(openaiPayload)
		req, _ := http.NewRequest("POST", AskCodiAPIBase+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+account.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if proxyURL != "" {
				c.proxyMgr.MarkProxyFailed(proxyURL)
			}
			continue
		}

		if resp.StatusCode != 200 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			errText := string(errBody)

			if isRequestTooLargeError(resp.StatusCode, errText) {
				fmt.Printf("[API-Anthropic] Request too large for account %s, returning error to client\n", account.Email)
				http.Error(w, errText, resp.StatusCode)
				return nil
			}
			if isTokenExhaustedError(resp.StatusCode, errText) {
				c.acctMgr.DisableAccount(account.ID, "Exhausted")
				fmt.Printf("[API-Anthropic] Account %s exhausted (attempt %d/%d), trying next...\n", account.Email, attempt+1, MaxRetryAccounts)
				continue
			}
			if resp.StatusCode == 401 {
				c.acctMgr.DisableAccount(account.ID, "Exhausted")
				fmt.Printf("[API-Anthropic] Account %s api_key invalid (401), marked Exhausted, trying next...\n", account.Email)
				continue
			}
			http.Error(w, errText, resp.StatusCode)
			return nil
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Convert OpenAI sync response to Anthropic format
		var openaiResp map[string]interface{}
		json.Unmarshal(respBody, &openaiResp)
		anthropicResp := openaiSyncToAnthropicJSON(openaiResp, origModel)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(anthropicResp)
		return nil
	}

	http.Error(w, `{"error":"All active accounts exhausted. Please register new accounts."}`, http.StatusServiceUnavailable)
	return nil
}

// doStreamAnthropicMessages handles streaming Anthropic /v1/messages by streaming from upstream OpenAI SSE
// and converting each chunk to Anthropic SSE events in real-time.
func (c *AskCodiClient) doStreamAnthropicMessages(openaiPayload map[string]interface{}, origModel string, w http.ResponseWriter) error {
	triedIDs := make([]int64, 0)
	for attempt := 0; attempt < MaxRetryAccounts; attempt++ {
		account, err := c.acctMgr.GetActiveAccount(triedIDs)
		if err != nil || account == nil {
			http.Error(w, `{"error":"No active accounts available"}`, http.StatusServiceUnavailable)
			return nil
		}
		triedIDs = append(triedIDs, account.ID)
		proxyURL, _ := c.proxyMgr.GetRandomProxy(nil)

		client, err := util.NewHTTPClient(proxyURL, 180*time.Second)
		if err != nil {
			continue
		}

		body, _ := json.Marshal(openaiPayload)
		req, _ := http.NewRequest("POST", AskCodiAPIBase+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+account.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", UserAgent)

		resp, err := client.Do(req)
		if err != nil {
			if proxyURL != "" {
				c.proxyMgr.MarkProxyFailed(proxyURL)
			}
			fmt.Printf("[API-Anthropic-Stream] Connection error: %v - proxy: %s\n", err, proxyURL)
			continue
		}

		if resp.StatusCode != 200 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			errText := string(errBody)

			fmt.Printf("[API-Anthropic-Stream] Account %s got HTTP %d: %s\n", account.Email, resp.StatusCode, errText)

			if isRequestTooLargeError(resp.StatusCode, errText) {
				fmt.Printf("[API-Anthropic-Stream] Request too large for account %s, returning error to client\n", account.Email)
				http.Error(w, errText, resp.StatusCode)
				return nil
			}
			if isTokenExhaustedError(resp.StatusCode, errText) {
				c.acctMgr.DisableAccount(account.ID, "Exhausted")
				fmt.Printf("[API-Anthropic-Stream] Account %s exhausted (attempt %d/%d), trying next...\n", account.Email, attempt+1, MaxRetryAccounts)
				continue
			}
			if resp.StatusCode == 401 {
				c.acctMgr.DisableAccount(account.ID, "Exhausted")
				fmt.Printf("[API-Anthropic-Stream] Account %s api_key invalid (401), marked Exhausted, trying next...\n", account.Email)
				continue
			}
			http.Error(w, errText, resp.StatusCode)
			return nil
		}

		// Set SSE headers and begin streaming
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(200)
		flusher, canFlush := w.(http.Flusher)

		flush := func() {
			if canFlush {
				flusher.Flush()
			}
		}

		msgID := fmt.Sprintf("msg_%s", randomHex(12))

		// Send message_start
		writeSSE(w, "message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id": msgID, "type": "message", "role": "assistant",
				"content": []interface{}{}, "model": origModel,
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		})
		flush()

		// State for translating OpenAI stream chunks to Anthropic events
		blockIndex := 0
		textBlockStarted := false
		toolCallBlockStarted := map[int]bool{}              // OpenAI tool_call index -> started
		toolCallToBlock := map[int]int{}                     // OpenAI tool_call index -> Anthropic block index
		finishReason := ""

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			choices, _ := chunk["choices"].([]interface{})
			if len(choices) == 0 {
				continue
			}
			choiceMap, _ := choices[0].(map[string]interface{})
			if choiceMap == nil {
				continue
			}

			delta, _ := choiceMap["delta"].(map[string]interface{})
			if delta == nil {
				delta = map[string]interface{}{}
			}

			// Check finish_reason
			if fr, ok := choiceMap["finish_reason"].(string); ok && fr != "" {
				finishReason = fr
			}

			// Handle text content
			if content, ok := delta["content"].(string); ok && content != "" {
				if !textBlockStarted {
					writeSSE(w, "content_block_start", map[string]interface{}{
						"type": "content_block_start", "index": blockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					})
					flush()
					textBlockStarted = true
				}
				writeSSE(w, "content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": content},
				})
				flush()
			}

			// Handle tool calls
			if tcs, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tc := range tcs {
					tcMap, _ := tc.(map[string]interface{})
					if tcMap == nil {
						continue
					}
					tcIdx := int(tcMap["index"].(float64))

					if !toolCallBlockStarted[tcIdx] {
						// Close text block if it was open and this is the first tool call
						if textBlockStarted && len(toolCallBlockStarted) == 0 {
							writeSSE(w, "content_block_stop", map[string]interface{}{
								"type": "content_block_stop", "index": blockIndex,
							})
							flush()
							blockIndex++
						}

						// Start new tool_use block
						fn, _ := tcMap["function"].(map[string]interface{})
						if fn == nil {
							fn = map[string]interface{}{}
						}
						toolName, _ := fn["name"].(string)
						toolID, _ := tcMap["id"].(string)

						toolCallToBlock[tcIdx] = blockIndex
						writeSSE(w, "content_block_start", map[string]interface{}{
							"type": "content_block_start", "index": blockIndex,
							"content_block": map[string]interface{}{
								"type": "tool_use", "id": toolID, "name": toolName, "input": map[string]interface{}{},
							},
						})
						flush()
						toolCallBlockStarted[tcIdx] = true

						// Send initial arguments if present
						if args, _ := fn["arguments"].(string); args != "" {
							writeSSE(w, "content_block_delta", map[string]interface{}{
								"type": "content_block_delta", "index": blockIndex,
								"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": args},
							})
							flush()
						}
					} else {
						// Continue existing tool call - send argument chunks
						fn, _ := tcMap["function"].(map[string]interface{})
						if fn != nil {
							if args, _ := fn["arguments"].(string); args != "" {
								bi := toolCallToBlock[tcIdx]
								writeSSE(w, "content_block_delta", map[string]interface{}{
									"type": "content_block_delta", "index": bi,
									"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": args},
								})
								flush()
							}
						}
					}
				}
			}
		}
		resp.Body.Close()

		// Close any open blocks
		if textBlockStarted && len(toolCallBlockStarted) == 0 {
			writeSSE(w, "content_block_stop", map[string]interface{}{
				"type": "content_block_stop", "index": blockIndex,
			})
			flush()
		}
		for tcIdx := range toolCallBlockStarted {
			bi := toolCallToBlock[tcIdx]
			writeSSE(w, "content_block_stop", map[string]interface{}{
				"type": "content_block_stop", "index": bi,
			})
			flush()
		}

		// Map finish_reason to Anthropic stop_reason
		stopReason := "end_turn"
		switch finishReason {
		case "tool_calls":
			stopReason = "tool_use"
		case "length":
			stopReason = "max_tokens"
		case "stop":
			stopReason = "end_turn"
		}

		writeSSE(w, "message_delta", map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]interface{}{"stop_reason": stopReason},
			"usage": map[string]interface{}{"output_tokens": 0},
		})
		flush()

		w.Write([]byte("event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"))
		flush()
		return nil
	}

	http.Error(w, `{"error":"All active accounts exhausted. Please register new accounts."}`, http.StatusServiceUnavailable)
	return nil
}

func writeSSE(w http.ResponseWriter, eventType string, data interface{}) {
	j, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(j))
}

// openaiSyncToAnthropicJSON converts an OpenAI sync response to Anthropic JSON format (non-streaming).
func openaiSyncToAnthropicJSON(resp map[string]interface{}, origModel string) map[string]interface{} {
	msgID := fmt.Sprintf("msg_%s", randomHex(12))

	choices, _ := resp["choices"].([]interface{})
	var choice map[string]interface{}
	if len(choices) > 0 {
		choice, _ = choices[0].(map[string]interface{})
	}
	if choice == nil {
		choice = map[string]interface{}{}
	}
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		message = map[string]interface{}{}
	}

	content := []interface{}{}
	if text, _ := message["content"].(string); text != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			tcMap, _ := tc.(map[string]interface{})
			if tcMap == nil {
				continue
			}
			fn, _ := tcMap["function"].(map[string]interface{})
			if fn == nil {
				continue
			}
			var input interface{}
			if args, _ := fn["arguments"].(string); args != "" {
				json.Unmarshal([]byte(args), &input)
			}
			if input == nil {
				input = map[string]interface{}{}
			}
			content = append(content, map[string]interface{}{
				"type": "tool_use", "id": tcMap["id"], "name": fn["name"], "input": input,
			})
		}
	}

	finishReason, _ := choice["finish_reason"].(string)
	stopReason := "end_turn"
	switch finishReason {
	case "tool_calls":
		stopReason = "tool_use"
	case "length":
		stopReason = "max_tokens"
	}

	return map[string]interface{}{
		"id": msgID, "type": "message", "role": "assistant",
		"content": content, "model": origModel,
		"stop_reason": stopReason, "stop_sequence": nil,
		"usage": resp["usage"],
	}
}

// translateAnthropicToOpenAI converts an Anthropic messages payload to OpenAI format.
func translateAnthropicToOpenAI(payload map[string]interface{}) map[string]interface{} {
	openai := map[string]interface{}{
		"model":      payload["model"],
		"messages":   []interface{}{},
		"max_tokens": payload["max_tokens"],
		"stream":     false,
	}
	if openai["max_tokens"] == nil {
		openai["max_tokens"] = 4096
	}
	if t, ok := payload["temperature"]; ok {
		openai["temperature"] = t
	}
	if t, ok := payload["top_p"]; ok {
		openai["top_p"] = t
	}

	messages := openai["messages"].([]interface{})

	// System message
	if sys, ok := payload["system"]; ok {
		sysText := ""
		switch v := sys.(type) {
		case string:
			sysText = v
		case []interface{}:
			for _, block := range v {
				if b, ok := block.(map[string]interface{}); ok && b["type"] == "text" {
					if t, ok := b["text"].(string); ok {
						sysText += t
					}
				}
			}
		}
		if sysText != "" {
			messages = append(messages, map[string]interface{}{"role": "system", "content": sysText})
		}
	}

	// Messages
	if msgs, ok := payload["messages"].([]interface{}); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			content := msg["content"]

			switch c := content.(type) {
			case string:
				messages = append(messages, map[string]interface{}{"role": role, "content": c})
			case []interface{}:
				openContent := []interface{}{}
				toolCalls := []interface{}{}

				for _, block := range c {
					b, ok := block.(map[string]interface{})
					if !ok {
						continue
					}
					blockType, _ := b["type"].(string)

					switch blockType {
					case "text":
						openContent = append(openContent, map[string]interface{}{
							"type": "text", "text": b["text"],
						})
					case "image":
						source, _ := b["source"].(map[string]interface{})
						srcType, _ := source["type"].(string)
						if srcType == "base64" {
							mediaType, _ := source["media_type"].(string)
							if mediaType == "" {
								mediaType = "image/png"
							}
							data, _ := source["data"].(string)
							openContent = append(openContent, map[string]interface{}{
								"type":      "image_url",
								"image_url": map[string]string{"url": fmt.Sprintf("data:%s;base64,%s", mediaType, data)},
							})
						} else if srcType == "url" {
							openContent = append(openContent, map[string]interface{}{
								"type":      "image_url",
								"image_url": map[string]string{"url": source["url"].(string)},
							})
						}
					case "tool_use":
						inputJSON, _ := json.Marshal(b["input"])
						toolCalls = append(toolCalls, map[string]interface{}{
							"id":   b["id"],
							"type": "function",
							"function": map[string]interface{}{
								"name":      b["name"],
								"arguments": string(inputJSON),
							},
						})
					case "tool_result":
						resContent := ""
						switch rc := b["content"].(type) {
						case string:
							resContent = rc
						case []interface{}:
							for _, item := range rc {
								if im, ok := item.(map[string]interface{}); ok {
									if t, ok := im["text"].(string); ok {
										resContent += t
									}
								}
							}
						}
						messages = append(messages, map[string]interface{}{
							"role":         "tool",
							"tool_call_id": b["tool_use_id"],
							"content":      resContent,
						})
					}
				}

				if len(openContent) > 0 || len(toolCalls) > 0 {
					msgBody := map[string]interface{}{"role": role}
					if len(openContent) > 0 {
						hasImages := false
						for _, oc := range openContent {
							if ocm, ok := oc.(map[string]interface{}); ok && ocm["type"] == "image_url" {
								hasImages = true
								break
							}
						}
						if len(openContent) == 1 && !hasImages {
							if ocm, ok := openContent[0].(map[string]interface{}); ok && ocm["type"] == "text" {
								msgBody["content"] = ocm["text"]
							} else {
								msgBody["content"] = openContent
							}
						} else {
							msgBody["content"] = openContent
						}
					}
					if len(toolCalls) > 0 {
						msgBody["tool_calls"] = toolCalls
					}
					messages = append(messages, msgBody)
				}
			}
		}
	}

	openai["messages"] = messages

	// Tools
	if tools, ok := payload["tools"].([]interface{}); ok {
		openaiTools := []interface{}{}
		for _, t := range tools {
			tool, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			openaiTools = append(openaiTools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tool["name"],
					"description": tool["description"],
					"parameters":  tool["input_schema"],
				},
			})
		}
		openai["tools"] = openaiTools
	}

	return openai
}

