package judge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/judge"
)

func TestOpenAICompatibleJudgeParsesJSONDecision(t *testing.T) {
	var gotModel string
	var gotBody string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret" {
			t.Fatalf("Authorization = %q", auth)
		}
		if r.URL.String() != "https://llm.example/v1/chat/completions" {
			t.Fatalf("url = %q", r.URL.String())
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(data)
		var req struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = req.Model
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(`{
				"choices":[{"message":{"content":"{\"resolved\":true,\"country\":\"FR\",\"town\":\"PARIS\",\"reason\":\"postcode and town agree\"}"}}]
			}`)),
			Header: make(http.Header),
		}, nil
	})
	client, err := judge.NewOpenAICompatible(judge.Config{
		APIKey:     "secret",
		BaseURL:    "https://llm.example",
		Model:      "judge-model",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible returned error: %v", err)
	}

	got, err := client.Judge(context.Background(), judge.Request{
		Input:     "77 RUE DE RIVOLI 75001 PARIS",
		Countries: []judge.CountryCandidate{{Code: "FR"}},
		Towns:     []judge.TownCandidate{{Name: "PARIS", CountryCode: "FR"}},
	})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}

	if gotModel != "judge-model" {
		t.Fatalf("model = %q, want judge-model", gotModel)
	}
	if !strings.Contains(gotBody, "Choose only from these candidates") {
		t.Fatalf("request body did not contain constraint prompt: %s", gotBody)
	}
	if !got.Resolved || got.Country != "FR" || got.Town != "PARIS" {
		t.Fatalf("decision = %#v, want resolved FR/PARIS", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
