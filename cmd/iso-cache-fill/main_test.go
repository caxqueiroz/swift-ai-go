package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/core"
	judgepkg "github.com/tipmarket/swift-ai/internal/judge"
)

func TestFillCacheCachesHighConfidenceSwiftRowsAsCRFPipeline(t *testing.T) {
	writer := &fakeCacheWriter{}
	judge := &fakeJudge{}
	opts := fillOptions{
		highConfidenceThreshold:   0.95,
		mediumConfidenceThreshold: 0.80,
		reviewPath:                "review.json",
	}

	summary, err := fillCache(
		context.Background(),
		[]core.AddressSample{{Text: "77 RUE DE RIVOLI 75001 PARIS"}},
		[]core.Result{resultWithScores("77 RUE DE RIVOLI 75001 PARIS", "FR", "PARIS", 0.97, 0.96)},
		writer,
		fakeFillEmbedder{},
		judge,
		opts,
	)
	if err != nil {
		t.Fatalf("fillCache returned error: %v", err)
	}

	if summary.Cached != 1 || len(writer.entries) != 1 {
		t.Fatalf("cached summary=%#v entries=%d, want one cached row", summary, len(writer.entries))
	}
	if writer.entries[0].Source != cache.SourceCRFPipeline {
		t.Fatalf("source = %q, want %q", writer.entries[0].Source, cache.SourceCRFPipeline)
	}
	if judge.calls != 0 {
		t.Fatalf("judge calls = %d, want 0 for high-confidence Swift row", judge.calls)
	}
}

func TestFillCacheUsesLLMJudgeForUncertainRowsAndStoresLLMAssisted(t *testing.T) {
	writer := &fakeCacheWriter{}
	judge := &fakeJudge{decision: judgepkg.Decision{Resolved: true, Country: "FR", Town: "PARIS", Reason: "postcode and town match"}}
	opts := fillOptions{
		highConfidenceThreshold:   0.95,
		mediumConfidenceThreshold: 0.80,
		reviewPath:                "review.json",
	}

	summary, err := fillCache(
		context.Background(),
		[]core.AddressSample{{Text: "77 RUE DE RIVOLI 75001 PARIS"}},
		[]core.Result{resultWithScores("77 RUE DE RIVOLI 75001 PARIS", "FR", "PARIS", 0.88, 0.86)},
		writer,
		fakeFillEmbedder{},
		judge,
		opts,
	)
	if err != nil {
		t.Fatalf("fillCache returned error: %v", err)
	}

	if judge.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", judge.calls)
	}
	if summary.Cached != 1 || len(writer.entries) != 1 {
		t.Fatalf("cached summary=%#v entries=%d, want one cached row", summary, len(writer.entries))
	}
	if writer.entries[0].Source != cache.SourceLLMAssisted {
		t.Fatalf("source = %q, want %q", writer.entries[0].Source, cache.SourceLLMAssisted)
	}
	if writer.entries[0].Structured.Country != "FR" || writer.entries[0].Structured.Town != "PARIS" {
		t.Fatalf("structured = %#v, want FR/PARIS", writer.entries[0].Structured)
	}
	if len(summary.ReviewRows) != 0 {
		t.Fatalf("review rows = %#v, want none after valid judge decision", summary.ReviewRows)
	}
}

func TestFillCacheExportsReviewWhenJudgeRejectsDecision(t *testing.T) {
	writer := &fakeCacheWriter{}
	judge := &fakeJudge{decision: judgepkg.Decision{Resolved: true, Country: "FR", Town: "LYON"}}
	opts := fillOptions{
		highConfidenceThreshold:   0.95,
		mediumConfidenceThreshold: 0.80,
		reviewPath:                "review.json",
	}

	summary, err := fillCache(
		context.Background(),
		[]core.AddressSample{{Text: "77 RUE DE RIVOLI 75001 PARIS"}},
		[]core.Result{resultWithScores("77 RUE DE RIVOLI 75001 PARIS", "FR", "PARIS", 0.88, 0.86)},
		writer,
		fakeFillEmbedder{},
		judge,
		opts,
	)
	if err != nil {
		t.Fatalf("fillCache returned error: %v", err)
	}

	if summary.Cached != 0 || len(writer.entries) != 0 {
		t.Fatalf("cached summary=%#v entries=%d, want no cached row", summary, len(writer.entries))
	}
	if len(summary.ReviewRows) != 1 || summary.ReviewRows[0].Reason != "judge_unresolved" {
		t.Fatalf("review rows = %#v, want judge_unresolved", summary.ReviewRows)
	}
}

func TestOpenSampleSourceReadsDataAddrDirectoryWithCountryFilter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sg", "countrywide-addresses-country.geojson"),
		`{"type":"Feature","properties":{"number":"1","street":"PARK STREET","postcode":"018928"}}`+"\n")
	writeFile(t, filepath.Join(root, "sg", "countrywide-buildings-country.geojson"),
		`{"type":"Feature","properties":{"number":"2","street":"BUILDING STREET"}}`+"\n")
	writeFile(t, filepath.Join(root, "us", "countrywide-addresses-country.geojson"),
		`{"type":"Feature","properties":{"number":"350","street":"FIFTH AVENUE","city":"NEW YORK"}}`+"\n")

	source, err := openSampleSource(root, fillOptions{countryFilter: "SG", maxRecords: 10})
	if err != nil {
		t.Fatalf("openSampleSource() error = %v", err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	got, err := source.NextBatch(10)
	if err != nil {
		t.Fatalf("NextBatch() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NextBatch() returned %d samples, want 1: %#v", len(got), got)
	}
	if got[0].Text != "1 PARK STREET\n018928" || got[0].SuggestedCountry != "SG" || !got[0].HasSuggestedCountry {
		t.Fatalf("sample = %#v, want SG OpenAddresses sample", got[0])
	}
}

func TestReviewFileWriterStreamsJSONArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	writer, err := newReviewFileWriter(path)
	if err != nil {
		t.Fatalf("newReviewFileWriter() error = %v", err)
	}
	if err := writer.Add(reviewRow{Input: "a", Country: "SG", Town: "SINGAPORE", Reason: "low_confidence"}); err != nil {
		t.Fatalf("Add() first error = %v", err)
	}
	if err := writer.Add(reviewRow{Input: "b", Country: "FR", Town: "PARIS", Reason: "missing_town"}); err != nil {
		t.Fatalf("Add() second error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read review file: %v", err)
	}
	var rows []reviewRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("review file is not valid JSON: %v\n%s", err, data)
	}
	if len(rows) != 2 || rows[0].Input != "a" || rows[1].Input != "b" {
		t.Fatalf("rows = %#v, want two streamed review rows", rows)
	}
}

func TestParseArgsAllowsLocalEmbeddingEndpointWithoutAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	_, err := parseArgs([]string{
		"--input-path", "addresses.csv",
		"--database-url", "postgresql://postgres:postgres@127.0.0.1:5432/swift_ai",
		"--embedding-base-url", "http://127.0.0.1:8090",
		"--embedding-model", "sentence-transformers/all-MiniLM-L6-v2",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs() error = %v, want local embedding endpoint without API key", err)
	}
}

type fakeCacheWriter struct {
	entries []cache.Entry
}

func (w *fakeCacheWriter) Upsert(_ context.Context, entry cache.Entry) error {
	w.entries = append(w.entries, entry)
	return nil
}

type fakeFillEmbedder struct{}

func (fakeFillEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return []float64{0.1, 0.2}, nil
}

type fakeJudge struct {
	decision judgepkg.Decision
	calls    int
}

func (j *fakeJudge) Judge(_ context.Context, request judgepkg.Request) (judgepkg.Decision, error) {
	j.calls++
	if len(request.Countries) == 0 || len(request.Towns) == 0 {
		return judgepkg.Decision{}, nil
	}
	return j.decision, nil
}

func resultWithScores(input string, country string, town string, countryScore float64, townScore float64) core.Result {
	return core.Result{
		CRFResult: core.CRFResult{
			Details:           core.Details{Content: input},
			PredictionsPerTag: map[core.Tag][]core.PredictionCRF{},
		},
		FuzzyResult: core.FuzzyResult{
			CountryMatches: []core.FuzzyMatch{{Origin: country, Possibility: country, FinalScore: countryScore}},
			TownMatches:    []core.FuzzyMatch{{Origin: country, Possibility: town, FinalScore: townScore}},
		},
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
}
