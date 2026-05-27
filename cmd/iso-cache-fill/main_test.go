package main

import (
	"context"
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
