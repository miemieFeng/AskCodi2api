package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/jonasen/askcodi-go/internal/service"
)

type ChatHandler struct {
	client *service.AskCodiClient
}

func NewChatHandler(client *service.AskCodiClient) *ChatHandler {
	return &ChatHandler{client: client}
}

func (h *ChatHandler) GetModels(w http.ResponseWriter, r *http.Request) {
	result, err := h.client.GetModels()
	if err != nil {
		http.Error(w, `{"error":"Failed to get models"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *ChatHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"Failed to read request body"}`, http.StatusBadRequest)
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
		return
	}
	h.client.ChatCompletionsHandler(payload, w)
}

func (h *ChatHandler) AnthropicMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"Failed to read request body"}`, http.StatusBadRequest)
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
		return
	}
	h.client.AnthropicMessages(payload, w)
}
