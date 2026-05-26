package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tipmarket/swift-ai/internal/api"
	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/cascade"
	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestHandlerAcceptsSingleTextRequest(t *testing.T) {
	converter := &fakeConverter{
		items: []cascade.Item{{
			Input:       "77 RUE DE RIVOLI 75001 PARIS",
			Structured:  structured.Address{Country: "FR", Town: "PARIS"},
			ServedBy:    cascade.ServedByStage2Pipeline,
			CacheSource: cache.SourceCRFPipeline,
		}},
	}
	handler := api.NewHandler(converter, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewBufferString(`{"text":"77 RUE DE RIVOLI 75001 PARIS"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(converter.requests) != 1 || converter.requests[0].Text != "77 RUE DE RIVOLI 75001 PARIS" {
		t.Fatalf("requests = %#v", converter.requests)
	}
	var body struct {
		Items []cascade.Item `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Structured.Country != "FR" || body.Items[0].Structured.Town != "PARIS" {
		t.Fatalf("response items = %#v", body.Items)
	}
}

func TestHandlerAcceptsBatchTextRequest(t *testing.T) {
	converter := &fakeConverter{items: []cascade.Item{
		{Input: "one", Structured: structured.Address{Country: "US"}},
		{Input: "two", Structured: structured.Address{Country: "FR"}},
	}}
	handler := api.NewHandler(converter, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewBufferString(`{"items":[{"text":"one"},{"text":"two"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(converter.requests) != 2 {
		t.Fatalf("requests = %#v, want 2", converter.requests)
	}
}

func TestHandlerRejectsCountryHints(t *testing.T) {
	handler := api.NewHandler(&fakeConverter{}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewBufferString(`{"text":"77 RUE DE RIVOLI","suggested_country":"FR"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("unknown field")) {
		t.Fatalf("body = %s, want unknown field error", rec.Body.String())
	}
}

func TestHandlerRejectsTrailingJSON(t *testing.T) {
	handler := api.NewHandler(&fakeConverter{}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewBufferString(`{"text":"one"}{"text":"two"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerRejectsNonPost(t *testing.T) {
	handler := api.NewHandler(&fakeConverter{}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodGet, "/convert", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandlerReturnsInternalErrorWhenConverterFails(t *testing.T) {
	handler := api.NewHandler(&fakeConverter{err: errors.New("pipeline failed")}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewBufferString(`{"text":"77 RUE DE RIVOLI"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

type fakeConverter struct {
	items    []cascade.Item
	err      error
	requests []cascade.Request
}

func (c *fakeConverter) Convert(_ context.Context, requests []cascade.Request) ([]cascade.Item, error) {
	c.requests = append(c.requests, requests...)
	if c.err != nil {
		return nil, c.err
	}
	return c.items, nil
}
