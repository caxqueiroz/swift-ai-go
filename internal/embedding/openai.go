package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dimensions int
	HTTPClient *http.Client
}

type OpenAICompatible struct {
	apiKey     string
	baseURL    string
	model      string
	dimensions int
	client     *http.Client
}

func NewOpenAICompatible(cfg Config) (*OpenAICompatible, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("embedding model is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse embedding base url: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenAICompatible{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
		client:     client,
	}, nil
}

func (c *OpenAICompatible) Embed(ctx context.Context, text string) ([]float64, error) {
	if c == nil {
		return nil, errors.New("embedding client is nil")
	}
	body := embeddingRequest{
		Model: c.model,
		Input: text,
	}
	if c.dimensions > 0 {
		body.Dimensions = c.dimensions
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingEndpoint(c.baseURL), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call embedding endpoint: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned status %d", resp.StatusCode)
	}

	var payload embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding response did not contain an embedding")
	}
	return payload.Data[0].Embedding, nil
}

func embeddingEndpoint(baseURL string) string {
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/embeddings"
	}
	return baseURL + "/v1/embeddings"
}

type embeddingRequest struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
}
