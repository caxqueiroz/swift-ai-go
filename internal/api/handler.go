package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tipmarket/swift-ai/internal/cascade"
)

type Converter interface {
	Convert(ctx context.Context, requests []cascade.Request) ([]cascade.Item, error)
}

type Handler struct {
	converter Converter
	logger    *slog.Logger
}

func NewHandler(converter Converter, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{converter: converter, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method must be POST")
		return
	}
	if h.converter == nil {
		writeError(w, http.StatusInternalServerError, "internal", "converter is not configured")
		return
	}

	requests, err := decodeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}

	items, err := h.converter.Convert(r.Context(), requests)
	if err != nil {
		h.logger.Error("convert request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "conversion failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response{Items: items}); err != nil {
		h.logger.Error("write convert response", "error", err)
	}
}

type requestBody struct {
	Text  string        `json:"text"`
	Items []requestItem `json:"items"`
}

type requestItem struct {
	Text string `json:"text"`
}

type response struct {
	Items []cascade.Item `json:"items"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeRequest(r *http.Request) ([]cascade.Request, error) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var body requestBody
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("request body must contain one json object")
	}
	if strings.TrimSpace(body.Text) != "" && len(body.Items) > 0 {
		return nil, errors.New("use either text or items, not both")
	}
	if strings.TrimSpace(body.Text) != "" {
		return []cascade.Request{{Text: body.Text}}, nil
	}
	if len(body.Items) == 0 {
		return nil, errors.New("text or items is required")
	}

	requests := make([]cascade.Request, len(body.Items))
	for i, item := range body.Items {
		if strings.TrimSpace(item.Text) == "" {
			return nil, fmt.Errorf("items[%d].text is required", i)
		}
		requests[i] = cascade.Request{Text: item.Text}
	}
	return requests, nil
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
	})
}
