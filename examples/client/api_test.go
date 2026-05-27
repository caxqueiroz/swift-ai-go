package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestConvertBatchCallsAPIAndMapsRows(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/convert" {
			t.Fatalf("request = %s %s, want POST /convert", r.Method, r.URL.Path)
		}
		var request convertRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.Items) != 2 || request.Items[0].Text != "1 PARK STREET" || request.Items[1].Text != "2 RIVOLI" {
			t.Fatalf("request items = %#v", request.Items)
		}
		body := `{"items":[
			{"input":"1 PARK STREET","structured":{"country":"SG","town":"SINGAPORE"},"served_by":"stage2_pipeline","resolution_status":"resolved","confidence_band":"high"},
			{"input":"2 RIVOLI","structured":{"country":"FR","town":"PARIS"},"served_by":"stage1_cache","resolution_status":"resolved","confidence_band":"high"}
		]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}, nil
	})}

	rows, err := convertBatch(t.Context(), client, "http://127.0.0.1:8080/convert", []addressRecord{
		{ID: "sg-1", Address: "1 PARK STREET", Country: "SG", Town: "SINGAPORE", Stratum: "SG:common"},
		{ID: "fr-1", Address: "2 RIVOLI", Country: "FR", Town: "PARIS", Stratum: "FR:common"},
	}, inputModeAddress)
	if err != nil {
		t.Fatalf("convertBatch() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ExpectedCountry != "SG" || rows[0].ActualCountry != "SG" || rows[0].ServedBy != "stage2_pipeline" {
		t.Fatalf("first row = %#v", rows[0])
	}
	if rows[1].ExpectedTown != "PARIS" || rows[1].ActualTown != "PARIS" || rows[1].ServedBy != "stage1_cache" {
		t.Fatalf("second row = %#v", rows[1])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
