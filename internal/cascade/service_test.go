package cascade_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/cascade"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestConvertUsesTrustedNearExactCacheHit(t *testing.T) {
	store := &fakeStore{
		candidates: []cache.Candidate{{
			Entry: cache.Entry{
				RawAddress:        "77 RUE DE RIVOLI 75001 PARIS",
				NormalizedAddress: cascade.NormalizeAddress("77 RUE DE RIVOLI 75001 PARIS"),
				Structured:        structured.Address{AddressLine: "77 RUE DE RIVOLI 75001 PARIS", Country: "FR", Town: "PARIS"},
				Source:            cache.SourceCRFPipeline,
			},
			SemanticScore: 0.94,
		}},
	}
	pipeline := &fakePipeline{}
	service := cascade.NewService(pipeline, cascade.WithCache(store), cascade.WithEmbedder(fakeEmbedder{}))

	got, err := service.Convert(context.Background(), []cascade.Request{{Text: "77 RUE DE RIVOLI 75001 PARIS"}})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	if pipeline.calls != 0 {
		t.Fatalf("pipeline calls = %d, want 0", pipeline.calls)
	}
	if got[0].ServedBy != cascade.ServedByStage1Cache {
		t.Fatalf("ServedBy = %q, want %q", got[0].ServedBy, cascade.ServedByStage1Cache)
	}
	if got[0].Structured.Country != "FR" || got[0].Structured.Town != "PARIS" {
		t.Fatalf("Structured = %#v, want FR/PARIS", got[0].Structured)
	}
	if got[0].CacheSource != cache.SourceCRFPipeline {
		t.Fatalf("CacheSource = %q, want %q", got[0].CacheSource, cache.SourceCRFPipeline)
	}
	if got[0].SemanticScore == nil || *got[0].SemanticScore != 0.94 {
		t.Fatalf("SemanticScore = %#v, want 0.94", got[0].SemanticScore)
	}
	if got[0].LexicalScore == nil || *got[0].LexicalScore < 0.85 {
		t.Fatalf("LexicalScore = %#v, want high lexical score", got[0].LexicalScore)
	}
}

func TestConvertFallsBackWhenLexicalGateFailsAndWritesPipelineResult(t *testing.T) {
	store := &fakeStore{
		candidates: []cache.Candidate{{
			Entry: cache.Entry{
				RawAddress:        "SAN FERNANDO PHILIPPINES",
				NormalizedAddress: cascade.NormalizeAddress("SAN FERNANDO PHILIPPINES"),
				Structured:        structured.Address{Country: "PH", Town: "SAN FERNANDO"},
				Source:            cache.SourceCRFPipeline,
			},
			SemanticScore: 0.96,
		}},
	}
	pipeline := &fakePipeline{
		results: []core.Result{pipelineResult("SAN PO KONG HONG KONG", "HK", "SAN PO KONG")},
	}
	service := cascade.NewService(pipeline, cascade.WithCache(store), cascade.WithEmbedder(fakeEmbedder{}))

	got, err := service.Convert(context.Background(), []cascade.Request{{Text: "SAN PO KONG HONG KONG"}})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	if pipeline.calls != 1 {
		t.Fatalf("pipeline calls = %d, want 1", pipeline.calls)
	}
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(store.upserts))
	}
	if store.upserts[0].Source != cache.SourceCRFPipeline {
		t.Fatalf("upsert source = %q, want %q", store.upserts[0].Source, cache.SourceCRFPipeline)
	}
	if got[0].Structured.Country != "HK" || got[0].Structured.Town != "SAN PO KONG" {
		t.Fatalf("Structured = %#v, want HK/SAN PO KONG", got[0].Structured)
	}
	if got[0].ServedBy != cascade.ServedByStage2Pipeline {
		t.Fatalf("ServedBy = %q, want %q", got[0].ServedBy, cascade.ServedByStage2Pipeline)
	}
	if got[0].FallbackReason != cascade.FallbackLexicalGateFailed {
		t.Fatalf("FallbackReason = %q, want %q", got[0].FallbackReason, cascade.FallbackLexicalGateFailed)
	}
	if len(pipeline.samples) != 1 || pipeline.samples[0].HasSuggestedCountry || pipeline.samples[0].ForceSuggestedCountry {
		t.Fatalf("pipeline sample = %#v, want text-only sample without country hints", pipeline.samples)
	}
}

func TestConvertFallsBackForSonnetSeedByDefault(t *testing.T) {
	store := &fakeStore{
		candidates: []cache.Candidate{{
			Entry: cache.Entry{
				RawAddress:        "350 FIFTH AVENUE NEW YORK NY 10118",
				NormalizedAddress: cascade.NormalizeAddress("350 FIFTH AVENUE NEW YORK NY 10118"),
				Structured:        structured.Address{Country: "US", Town: "NEW YORK"},
				Source:            cache.SourceSonnetSeed,
			},
			SemanticScore: 0.99,
		}},
	}
	pipeline := &fakePipeline{
		results: []core.Result{pipelineResult("350 FIFTH AVENUE NEW YORK NY 10118", "US", "NEW YORK")},
	}
	service := cascade.NewService(pipeline, cascade.WithCache(store), cascade.WithEmbedder(fakeEmbedder{}))

	got, err := service.Convert(context.Background(), []cascade.Request{{Text: "350 FIFTH AVENUE NEW YORK NY 10118"}})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	if got[0].ServedBy != cascade.ServedByStage2Pipeline {
		t.Fatalf("ServedBy = %q, want Stage 2", got[0].ServedBy)
	}
	if got[0].FallbackReason != cascade.FallbackUntrustedCacheSource {
		t.Fatalf("FallbackReason = %q, want %q", got[0].FallbackReason, cascade.FallbackUntrustedCacheSource)
	}
}

func TestConvertBatchPreservesOrderAcrossCacheAndPipeline(t *testing.T) {
	store := &fakeStore{
		candidatesByText: map[string][]cache.Candidate{
			cascade.NormalizeAddress("77 RUE DE RIVOLI 75001 PARIS"): {{
				Entry: cache.Entry{
					RawAddress:        "77 RUE DE RIVOLI 75001 PARIS",
					NormalizedAddress: cascade.NormalizeAddress("77 RUE DE RIVOLI 75001 PARIS"),
					Structured:        structured.Address{AddressLine: "77 RUE DE RIVOLI 75001 PARIS", Country: "FR", Town: "PARIS"},
					Source:            cache.SourceCRFPipeline,
				},
				SemanticScore: 0.95,
			}},
		},
	}
	pipeline := &fakePipeline{
		results: []core.Result{
			pipelineResult("350 FIFTH AVENUE NEW YORK NY 10118", "US", "NEW YORK"),
			pipelineResult("ACME GMBH 10115 BERLIN", "DE", "BERLIN"),
		},
	}
	service := cascade.NewService(pipeline, cascade.WithCache(store), cascade.WithEmbedder(fakeEmbedder{}))

	got, err := service.Convert(context.Background(), []cascade.Request{
		{Text: "350 FIFTH AVENUE NEW YORK NY 10118"},
		{Text: "77 RUE DE RIVOLI 75001 PARIS"},
		{Text: "ACME GMBH 10115 BERLIN"},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	wantCountries := []string{"US", "FR", "DE"}
	for i, want := range wantCountries {
		if got[i].Structured.Country != want {
			t.Fatalf("item %d country = %q, want %q; all=%#v", i, got[i].Structured.Country, want, got)
		}
	}
	if pipeline.calls != 1 {
		t.Fatalf("pipeline calls = %d, want one batched Stage 2 call", pipeline.calls)
	}
	if len(pipeline.samples) != 2 {
		t.Fatalf("pipeline samples = %d, want 2 cache misses", len(pipeline.samples))
	}
}

func TestConvertReturnsPipelineError(t *testing.T) {
	pipeline := &fakePipeline{err: errors.New("model failed")}
	service := cascade.NewService(pipeline)

	_, err := service.Convert(context.Background(), []cascade.Request{{Text: "77 RUE DE RIVOLI"}})
	if err == nil {
		t.Fatal("Convert returned nil error, want pipeline error")
	}
}

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	return []float64{float64(len(text))}, nil
}

type fakeStore struct {
	candidates       []cache.Candidate
	candidatesByText map[string][]cache.Candidate
	upserts          []cache.Entry
}

func (s *fakeStore) Search(_ context.Context, normalizedAddress string, _ []float64, _ int) ([]cache.Candidate, error) {
	if s.candidatesByText != nil {
		return s.candidatesByText[normalizedAddress], nil
	}
	return s.candidates, nil
}

func (s *fakeStore) Upsert(_ context.Context, entry cache.Entry) error {
	s.upserts = append(s.upserts, entry)
	return nil
}

type fakePipeline struct {
	results []core.Result
	err     error
	calls   int
	samples []core.AddressSample
}

func (p *fakePipeline) Run(_ context.Context, samples []core.AddressSample) ([]core.Result, error) {
	p.calls++
	p.samples = append(p.samples, samples...)
	if p.err != nil {
		return nil, p.err
	}
	return p.results, nil
}

func pipelineResult(input string, country string, town string) core.Result {
	return core.Result{
		CRFResult: core.CRFResult{
			Details:           core.Details{Content: input},
			PredictionsPerTag: map[core.Tag][]core.PredictionCRF{},
		},
		FuzzyResult: core.FuzzyResult{
			CountryMatches: []core.FuzzyMatch{{Origin: country, Possibility: country, FinalScore: 0.91}},
			TownMatches:    []core.FuzzyMatch{{Origin: country, Possibility: town, FinalScore: 0.92}},
		},
	}
}
