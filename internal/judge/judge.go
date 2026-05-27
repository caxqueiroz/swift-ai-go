package judge

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

type CountryCandidate struct {
	Code        string  `json:"code"`
	Score       float64 `json:"score,omitempty"`
	Matched     string  `json:"matched,omitempty"`
	Possibility string  `json:"possibility,omitempty"`
}

type TownCandidate struct {
	Name        string  `json:"name"`
	CountryCode string  `json:"country_code"`
	Score       float64 `json:"score,omitempty"`
	Matched     string  `json:"matched,omitempty"`
}

type Request struct {
	Input     string             `json:"input"`
	Countries []CountryCandidate `json:"countries"`
	Towns     []TownCandidate    `json:"towns"`
}

type Decision struct {
	Resolved bool   `json:"resolved"`
	Country  string `json:"country,omitempty"`
	Town     string `json:"town,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type Client interface {
	Judge(ctx context.Context, request Request) (Decision, error)
}

func ValidateDecision(request Request, decision Decision) (Decision, bool) {
	decision.Country = strings.ToUpper(strings.TrimSpace(decision.Country))
	decision.Town = strings.ToUpper(strings.TrimSpace(decision.Town))
	if !decision.Resolved {
		return decision, false
	}
	if decision.Country == "" || decision.Town == "" {
		return decision, false
	}
	if !countryAllowed(request.Countries, decision.Country) {
		return decision, false
	}
	if !townAllowed(request.Towns, decision.Town, decision.Country) {
		return decision, false
	}
	return decision, true
}

func countryAllowed(candidates []CountryCandidate, code string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.Code, code) {
			return true
		}
	}
	return false
}

func townAllowed(candidates []TownCandidate, town string, country string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.Name, town) && strings.EqualFold(candidate.CountryCode, country) {
			return true
		}
	}
	return false
}

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type OpenAICompatible struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

func NewOpenAICompatible(cfg Config) (*OpenAICompatible, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("api key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("judge model is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse judge base url: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenAICompatible{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		model:   cfg.Model,
		client:  client,
	}, nil
}

func (c *OpenAICompatible) Judge(ctx context.Context, request Request) (Decision, error) {
	if c == nil {
		return Decision{}, errors.New("judge client is nil")
	}
	data, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "You resolve postal address country and town. Choose only from these candidates. If none fit, return unresolved. Return only JSON with resolved, country, town, reason.",
			},
			{
				Role:    "user",
				Content: prompt(request),
			},
		},
		ResponseFormat: responseFormat{Type: "json_object"},
		Temperature:    0,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("encode judge request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return Decision{}, fmt.Errorf("create judge request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("call judge endpoint: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Decision{}, fmt.Errorf("judge endpoint returned status %d", resp.StatusCode)
	}

	var payload chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Decision{}, fmt.Errorf("decode judge response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return Decision{}, errors.New("judge response contained no choices")
	}
	var decision Decision
	if err := json.Unmarshal([]byte(payload.Choices[0].Message.Content), &decision); err != nil {
		return Decision{}, fmt.Errorf("decode judge decision: %w", err)
	}
	return decision, nil
}

func prompt(request Request) string {
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Sprintf("Choose only from these candidates. Input: %s", request.Input)
	}
	return "Choose only from these candidates. Do not invent countries or towns.\n" + string(data)
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Temperature    float64        `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}
