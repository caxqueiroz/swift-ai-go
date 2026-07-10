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
			{"input":"1 PARK STREET","structured":{"country":"SG","town":"SINGAPORE","country_confidence":0.98,"town_confidence":0.97},"served_by":"stage2_pipeline","resolution_status":"resolved","confidence_band":"high","quality_score":0.97,"quality_reason":"high_confidence"},
			{"input":"2 RIVOLI","structured":{"country":"FR","town":"PARIS","country_confidence":0.91,"town_confidence":0.88},"served_by":"stage1_cache","resolution_status":"needs_review","confidence_band":"medium","quality_score":0.88,"quality_reason":"confidence_below_high"}
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
	if rows[0].ActualCountryConfidence != 0.98 || rows[0].ActualTownConfidence != 0.97 || rows[0].QualityScore != 0.97 || rows[0].QualityReason != "high_confidence" {
		t.Fatalf("first row diagnostics = %#v", rows[0])
	}
	if rows[1].ExpectedTown != "PARIS" || rows[1].ActualTown != "PARIS" || rows[1].ServedBy != "stage1_cache" {
		t.Fatalf("second row = %#v", rows[1])
	}
}

func TestConvertBatchAddsCanonicalOutcomeFields(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"items":[
			{"input":"4162\nالعين\nSA","structured":{"country":"SA","town":"L'YN","country_confidence":0.92,"town_confidence":0.90},"served_by":"stage2_pipeline","resolution_status":"needs_review","confidence_band":"medium","quality_score":0.90,"quality_reason":"confidence_below_high"}
		]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}, nil
	})}

	rows, err := convertBatch(t.Context(), client, "http://127.0.0.1:8080/convert", []addressRecord{
		{ID: "sa-1", Address: "4162\nالعين\nSA", Country: "SA", Town: "العين", Stratum: "SA:common"},
	}, inputModeAddress)
	if err != nil {
		t.Fatalf("convertBatch() error = %v", err)
	}
	row := rows[0]
	if row.ExpectedTownCanonical == "" || row.ActualTownCanonical == "" {
		t.Fatalf("canonical fields not populated: %#v", row)
	}
	if !row.CountryCorrect || !row.TownCorrect || !row.BothCorrect {
		t.Fatalf("outcome fields = country %t town %t both %t; row %#v", row.CountryCorrect, row.TownCorrect, row.BothCorrect, row)
	}
	if row.Committed {
		t.Fatalf("committed = true, want false for needs_review/medium")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
