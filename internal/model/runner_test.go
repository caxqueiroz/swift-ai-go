package model

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

type fakeInferenceEngine struct {
	gotTokenIDs [][]int64
	gotMask     [][]bool
	emissions   [][][]float64
	countries   [][]float64
	err         error
}

func (e *fakeInferenceEngine) Run(_ context.Context, tokenIDs [][]int64, mask [][]bool) ([][][]float64, [][]float64, error) {
	e.gotTokenIDs = tokenIDs
	e.gotMask = mask
	return e.emissions, e.countries, e.err
}

func TestRunnerTagAggregatesCRFOutputAndCountryPrediction(t *testing.T) {
	tokenizer, err := NewCharacterTokenizer([]string{"U", "S", " ", "N", "Y"})
	if err != nil {
		t.Fatalf("NewCharacterTokenizer() error = %v", err)
	}

	tags := []core.BIOTag{
		{BIO: core.BioOther, Tag: core.TagOther},
		{BIO: core.BioBefore, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
		{BIO: core.BioBefore, Tag: core.TagTown},
		{BIO: core.BioInside, Tag: core.TagTown},
	}
	engine := &fakeInferenceEngine{
		emissions: [][][]float64{{
			{0, 5, 0, 0, 0},
			{0, 0, 5, 0, 0},
			{5, 0, 0, 0, 0},
			{0, 0, 0, 5, 0},
			{0, 0, 0, 0, 5},
			{0, 0, 0, 0, 0},
		}},
		countries: [][]float64{{1, 3}},
	}

	runner, err := NewRunner(tokenizer, engine, CRF{}, RunnerConfig{
		MaxSequenceLength: 6,
		BIOTagsToKeep:     tags,
		TagsToKeep:        []core.Tag{core.TagCountry, core.TagTown, core.TagPostalCode},
		IDToCountry:       map[int]string{0: "DE", 1: "US"},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	got, err := runner.Tag(context.Background(), "US NY")
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}

	wantTokenIDs := [][]int64{{2, 3, 4, 5, 6, int64(tokenizer.PadIndex())}}
	if !reflect.DeepEqual(engine.gotTokenIDs, wantTokenIDs) {
		t.Fatalf("tokenIDs = %#v, want %#v", engine.gotTokenIDs, wantTokenIDs)
	}
	wantMask := [][]bool{{true, true, true, true, true, false}}
	if !reflect.DeepEqual(engine.gotMask, wantMask) {
		t.Fatalf("mask = %#v, want %#v", engine.gotMask, wantMask)
	}

	wantDetails := core.Details{
		Content:               "US NY",
		CountryCode:           "US",
		CountryCodeConfidence: 0.8807970779778823,
		Spans: []core.TaggedSpan{
			{Start: 0, End: 2, Tag: core.TagCountry},
			{Start: 2, End: 3, Tag: core.TagOther},
			{Start: 3, End: 5, Tag: core.TagTown},
		},
	}
	if !detailsEqualApprox(got.Details, wantDetails) {
		t.Fatalf("Details = %#v, want %#v", got.Details, wantDetails)
	}

	wantCountryPredictions := []core.PredictionCRF{{
		TaggedSpan: core.TaggedSpan{Start: 0, End: 2, Tag: core.TagCountry},
		Prediction: "US",
	}}
	if !predictionsEqualIgnoringConfidence(got.PredictionsPerTag[core.TagCountry], wantCountryPredictions) {
		t.Fatalf("country predictions = %#v, want %#v", got.PredictionsPerTag[core.TagCountry], wantCountryPredictions)
	}
	if confidence := got.PredictionsPerTag[core.TagCountry][0].Confidence; confidence < 0.9 || confidence > 1 {
		t.Fatalf("country prediction confidence = %f, want high marginal probability", confidence)
	}

	wantTownPredictions := []core.PredictionCRF{{
		TaggedSpan: core.TaggedSpan{Start: 3, End: 5, Tag: core.TagTown},
		Prediction: "NY",
	}}
	if !predictionsEqualIgnoringConfidence(got.PredictionsPerTag[core.TagTown], wantTownPredictions) {
		t.Fatalf("town predictions = %#v, want %#v", got.PredictionsPerTag[core.TagTown], wantTownPredictions)
	}
	if got.PredictionsPerTag[core.TagPostalCode] == nil {
		t.Fatal("postal code predictions = nil, want empty default slice")
	}
	if len(got.PredictionsPerTag[core.TagPostalCode]) != 0 {
		t.Fatalf("postal code predictions = %#v, want empty", got.PredictionsPerTag[core.TagPostalCode])
	}

	wantCountryEmissions := []float64{5, 5, 0, 0, 0, 0}
	if !reflect.DeepEqual(got.EmissionsPerTag[core.TagCountry], wantCountryEmissions) {
		t.Fatalf("country emissions = %#v, want %#v", got.EmissionsPerTag[core.TagCountry], wantCountryEmissions)
	}
	wantTownEmissions := []float64{0, 0, 0, 5, 5, 0}
	if !reflect.DeepEqual(got.EmissionsPerTag[core.TagTown], wantTownEmissions) {
		t.Fatalf("town emissions = %#v, want %#v", got.EmissionsPerTag[core.TagTown], wantTownEmissions)
	}
	if got.EmissionsPerTag[core.TagPostalCode] == nil || got.LogProbasPerTag[core.TagPostalCode] == nil {
		t.Fatalf("postal code defaults missing: emissions=%#v logprobas=%#v", got.EmissionsPerTag[core.TagPostalCode], got.LogProbasPerTag[core.TagPostalCode])
	}
	if got.LogProbasPerTag[core.TagCountry][0] < 0.9 || got.LogProbasPerTag[core.TagCountry][1] < 0.9 {
		t.Fatalf("country log probabilities = %#v, want high first two positions", got.LogProbasPerTag[core.TagCountry])
	}
}

func TestRunnerTagLeavesAbsentConfiguredTagSeriesZero(t *testing.T) {
	tokenizer, err := NewCharacterTokenizer([]string{"U", "S"})
	if err != nil {
		t.Fatalf("NewCharacterTokenizer() error = %v", err)
	}

	tags := []core.BIOTag{
		{BIO: core.BioOther, Tag: core.TagOther},
		{BIO: core.BioBefore, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
		{BIO: core.BioBefore, Tag: core.TagPostalCode},
		{BIO: core.BioInside, Tag: core.TagPostalCode},
	}
	engine := &fakeInferenceEngine{
		emissions: [][][]float64{{
			{0, 10, 0, 4, 0},
			{0, 0, 10, 0, 4},
		}},
		countries: [][]float64{{1}},
	}

	runner, err := NewRunner(tokenizer, engine, CRF{}, RunnerConfig{
		MaxSequenceLength: 2,
		BIOTagsToKeep:     tags,
		TagsToKeep:        []core.Tag{core.TagCountry, core.TagPostalCode},
		IDToCountry:       map[int]string{0: "US"},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	got, err := runner.Tag(context.Background(), "US")
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}

	if len(got.PredictionsPerTag[core.TagPostalCode]) != 0 {
		t.Fatalf("postal code predictions = %#v, want none", got.PredictionsPerTag[core.TagPostalCode])
	}
	wantZeroes := []float64{0, 0}
	if !reflect.DeepEqual(got.EmissionsPerTag[core.TagPostalCode], wantZeroes) {
		t.Fatalf("postal code emissions = %#v, want %#v", got.EmissionsPerTag[core.TagPostalCode], wantZeroes)
	}
	if !reflect.DeepEqual(got.LogProbasPerTag[core.TagPostalCode], wantZeroes) {
		t.Fatalf("postal code log probabilities = %#v, want %#v", got.LogProbasPerTag[core.TagPostalCode], wantZeroes)
	}
}

func detailsEqualApprox(got, want core.Details) bool {
	if got.Content != want.Content || got.CountryCode != want.CountryCode {
		return false
	}
	if math.Abs(got.CountryCodeConfidence-want.CountryCodeConfidence) > 1e-12 {
		return false
	}
	return reflect.DeepEqual(got.Spans, want.Spans)
}

func predictionsEqualIgnoringConfidence(got, want []core.PredictionCRF) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].TaggedSpan != want[i].TaggedSpan || got[i].Prediction != want[i].Prediction {
			return false
		}
	}
	return true
}
