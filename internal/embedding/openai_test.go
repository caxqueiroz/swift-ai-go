package embedding_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/tipmarket/swift-ai/internal/embedding"
)

func TestOpenAICompatibleClientParsesEmbeddingResponse(t *testing.T) {
	var gotModel string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = req.Model
		if r.URL.String() != "https://embeddings.example/v1/embeddings" {
			t.Fatalf("url = %q", r.URL.String())
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret" {
			t.Fatalf("Authorization = %q", auth)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.25,0.5]}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	client, err := embedding.NewOpenAICompatible(embedding.Config{
		APIKey:     "secret",
		BaseURL:    "https://embeddings.example",
		Model:      "embedding-model",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible returned error: %v", err)
	}

	got, err := client.Embed(t.Context(), "77 RUE DE RIVOLI")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}

	if gotModel != "embedding-model" {
		t.Fatalf("model = %q", gotModel)
	}
	if len(got) != 2 || got[0] != 0.25 || got[1] != 0.5 {
		t.Fatalf("embedding = %#v", got)
	}
}

func TestOpenAICompatibleAcceptsVersionedBaseURL(t *testing.T) {
	var gotURL string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.25,0.5]}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	client, err := embedding.NewOpenAICompatible(embedding.Config{
		APIKey:     "secret",
		BaseURL:    "https://api.openai.com/v1",
		Model:      "embedding-model",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible returned error: %v", err)
	}

	if _, err := client.Embed(t.Context(), "77 RUE DE RIVOLI"); err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if gotURL != "https://api.openai.com/v1/embeddings" {
		t.Fatalf("url = %q, want versioned embeddings endpoint", gotURL)
	}
}

func TestOpenAICompatibleAllowsLocalNoAuthEndpoint(t *testing.T) {
	var gotAuth string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.25,0.5]}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	client, err := embedding.NewOpenAICompatible(embedding.Config{
		BaseURL:    "http://127.0.0.1:8090",
		Model:      "sentence-transformers/all-MiniLM-L6-v2",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible returned error: %v", err)
	}

	if _, err := client.Embed(t.Context(), "77 RUE DE RIVOLI"); err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want no auth header for no-key endpoint", gotAuth)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
