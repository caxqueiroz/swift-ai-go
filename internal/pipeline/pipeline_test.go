package pipeline

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/postprocess"
	"github.com/tipmarket/swift-ai/internal/resources"
)

func TestRunCleansSamplesBeforeCallingRunnersAndPreservesMetadata(t *testing.T) {
	cfg := pipelineTestConfig()
	cfg.BatchSize = 10
	cfg.CRF.MaxSequenceLength = 100
	db := pipelineTestDB()
	model := &fakeModelRunner{}
	fuzzy := &fakeFuzzyRunner{}
	postcodes := &fakePostcodeRunner{}
	runner := New(cfg, &db, model,
		WithFuzzyRunner(fuzzy),
		WithPostcodeRunner(postcodes),
	)

	samples := []core.AddressSample{{
		Text:                  "café\\nstraße\r jp",
		SuggestedCountry:      "JP",
		HasSuggestedCountry:   true,
		ForceSuggestedCountry: true,
	}}

	got, err := runner.Run(context.Background(), samples)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantText := "CAFE\nSTRASSE JP"
	if !reflect.DeepEqual(model.batches, [][]string{{wantText}}) {
		t.Fatalf("model batches = %#v, want %#v", model.batches, [][]string{{wantText}})
	}
	if !reflect.DeepEqual(fuzzy.batches, [][]string{{wantText}}) {
		t.Fatalf("fuzzy batches = %#v, want %#v", fuzzy.batches, [][]string{{wantText}})
	}
	if !reflect.DeepEqual(postcodes.batches, [][]string{{wantText}}) {
		t.Fatalf("postcode batches = %#v, want %#v", postcodes.batches, [][]string{{wantText}})
	}
	if got[0].CRFResult.Details.Content != wantText {
		t.Fatalf("result CRF content = %q, want %q", got[0].CRFResult.Details.Content, wantText)
	}
	generated := findGeneratedSuggestedCountry(t, got[0].FuzzyResult.CountryMatches)
	wantGeneratedOffset := len(wantText) + 2
	if generated.Start != wantGeneratedOffset || generated.End != wantGeneratedOffset {
		t.Fatalf("generated suggested country span = [%d,%d], want cleaned sample offset %d", generated.Start, generated.End, wantGeneratedOffset)
	}
	if got[0].SuggestedCountry != "JP" || !got[0].HasSuggestedCountry || !got[0].ForceSuggestedCountry {
		t.Fatalf("metadata not preserved in result: %#v", got[0])
	}
}

func TestRunValidatesOriginalRuneLengthBeforeCleaning(t *testing.T) {
	cfg := pipelineTestConfig()
	cfg.CRF.MaxSequenceLength = 4
	db := pipelineTestDB()
	model := &fakeModelRunner{}
	runner := New(cfg, &db, model,
		WithFuzzyRunner(&fakeFuzzyRunner{}),
		WithPostcodeRunner(&fakePostcodeRunner{}),
	)

	_, err := runner.Run(context.Background(), []core.AddressSample{{Text: "ééééé"}})
	if err == nil {
		t.Fatal("Run returned nil error, want validation error")
	}
	if !strings.Contains(err.Error(), "sample 0") ||
		!strings.Contains(err.Error(), "length 5") ||
		!strings.Contains(err.Error(), "max sequence length 4") ||
		!strings.Contains(err.Error(), "ééééé") {
		t.Fatalf("validation error = %q, want sample index, rune length, max, and original text", err)
	}
	if len(model.batches) != 0 {
		t.Fatalf("model batches = %#v, want validation before model call", model.batches)
	}
}

func TestRunReturnsErrorForNilModelRunner(t *testing.T) {
	cfg := pipelineTestConfig()
	cfg.CRF.MaxSequenceLength = 100
	db := pipelineTestDB()
	runner := New(cfg, &db, nil,
		WithFuzzyRunner(&fakeFuzzyRunner{}),
		WithPostcodeRunner(&fakePostcodeRunner{}),
	)

	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("Run panicked with nil model runner: %v", recovered)
			}
		}()
		_, err = runner.Run(context.Background(), []core.AddressSample{{Text: "one"}})
	}()

	if err == nil {
		t.Fatal("Run returned nil error, want model runner dependency error")
	}
	if !strings.Contains(err.Error(), "model runner") {
		t.Fatalf("Run error = %q, want useful model runner context", err)
	}
}

func TestRunBatchesAndPreservesOutputOrder(t *testing.T) {
	cfg := pipelineTestConfig()
	cfg.BatchSize = 2
	cfg.CRF.MaxSequenceLength = 100
	db := pipelineTestDB()
	model := &fakeModelRunner{}
	fuzzy := &fakeFuzzyRunner{}
	postcodes := &fakePostcodeRunner{}
	runner := New(cfg, &db, model,
		WithFuzzyRunner(fuzzy),
		WithPostcodeRunner(postcodes),
	)

	samples := []core.AddressSample{
		{Text: "one"},
		{Text: "two"},
		{Text: "three"},
		{Text: "four"},
		{Text: "five"},
	}

	got, err := runner.Run(context.Background(), samples)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantBatches := [][]string{{"ONE", "TWO"}, {"THREE", "FOUR"}, {"FIVE"}}
	if !reflect.DeepEqual(model.batches, wantBatches) {
		t.Fatalf("model batches = %#v, want %#v", model.batches, wantBatches)
	}
	if !reflect.DeepEqual(fuzzy.batches, wantBatches) {
		t.Fatalf("fuzzy batches = %#v, want %#v", fuzzy.batches, wantBatches)
	}
	if !reflect.DeepEqual(postcodes.batches, wantBatches) {
		t.Fatalf("postcode batches = %#v, want %#v", postcodes.batches, wantBatches)
	}
	gotTexts := make([]string, len(got))
	for i := range got {
		gotTexts[i] = got[i].CRFResult.Details.Content
	}
	wantTexts := []string{"ONE", "TWO", "THREE", "FOUR", "FIVE"}
	if !reflect.DeepEqual(gotTexts, wantTexts) {
		t.Fatalf("result order = %#v, want %#v", gotTexts, wantTexts)
	}
}

func TestRunUsesDefaultBatchSizeWhenConfiguredBatchSizeIsNonPositive(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
	}{
		{name: "zero", batchSize: 0},
		{name: "negative", batchSize: -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := pipelineTestConfig()
			cfg.BatchSize = tt.batchSize
			cfg.CRF.MaxSequenceLength = 100
			db := pipelineTestDB()
			model := &fakeModelRunner{}
			runner := New(cfg, &db, model,
				WithFuzzyRunner(&fakeFuzzyRunner{}),
				WithPostcodeRunner(&fakePostcodeRunner{}),
			)

			samples := []core.AddressSample{{Text: "one"}, {Text: "two"}, {Text: "three"}}
			if _, err := runner.Run(context.Background(), samples); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			want := [][]string{{"ONE", "TWO", "THREE"}}
			if !reflect.DeepEqual(model.batches, want) {
				t.Fatalf("model batches = %#v, want default single batch %#v", model.batches, want)
			}
		})
	}
}

type fakeModelRunner struct {
	batches [][]string
}

func (r *fakeModelRunner) TagBatch(_ context.Context, data []string) ([]core.CRFResult, error) {
	r.batches = append(r.batches, append([]string(nil), data...))

	results := make([]core.CRFResult, len(data))
	for i, text := range data {
		results[i] = pipelineTestCRF(text)
	}
	return results, nil
}

type fakeFuzzyRunner struct {
	batches [][]string
}

func (r *fakeFuzzyRunner) MatchBatch(_ context.Context, data []string, samples []core.AddressSample) ([]core.FuzzyResult, error) {
	r.batches = append(r.batches, append([]string(nil), data...))
	if len(data) != len(samples) {
		return nil, fmt.Errorf("fake fuzzy length mismatch: data=%d samples=%d", len(data), len(samples))
	}
	return make([]core.FuzzyResult, len(data)), nil
}

type fakePostcodeRunner struct {
	batches [][]string
}

func (r *fakePostcodeRunner) MatchBatch(_ context.Context, data []string) ([][]core.PostcodeMatch, error) {
	r.batches = append(r.batches, append([]string(nil), data...))
	return make([][]core.PostcodeMatch, len(data)), nil
}

func pipelineTestConfig() config.Config {
	cfg := config.Default()
	cfg.PostProcessing.MinimalFinalScoreCountry = 0.01
	cfg.PostProcessing.MinimalFinalScoreTown = 0.01
	return cfg
}

func pipelineTestDB() resources.Database {
	return resources.Database{}
}

func pipelineTestCRF(text string) core.CRFResult {
	return core.CRFResult{
		Details: core.Details{
			Content: text,
		},
		PredictionsPerTag: map[core.Tag][]core.PredictionCRF{},
		EmissionsPerTag: map[core.Tag][]float64{
			core.TagCountry: pipelineTestSeries(len([]rune(text)), 0.5),
			core.TagTown:    pipelineTestSeries(len([]rune(text)), 0.5),
		},
		LogProbasPerTag: map[core.Tag][]float64{
			core.TagCountry: pipelineTestSeries(len([]rune(text)), 0.5),
			core.TagTown:    pipelineTestSeries(len([]rune(text)), 0.5),
		},
	}
}

func pipelineTestSeries(length int, value float64) []float64 {
	values := make([]float64, length)
	for i := range values {
		values[i] = value
	}
	return values
}

func findGeneratedSuggestedCountry(t *testing.T, matches []core.FuzzyMatch) core.FuzzyMatch {
	t.Helper()

	for _, match := range matches {
		for _, flag := range match.Flags {
			if flag == core.FlagGeneratedBySuggestedCountry {
				return match
			}
		}
	}

	t.Fatalf("country matches = %#v, want generated suggested country", matches)
	return core.FuzzyMatch{}
}

var _ PostprocessRunner = postprocess.NewRunner(config.Default(), resources.Database{})
