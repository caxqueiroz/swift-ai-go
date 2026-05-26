package structured_test

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestFromResultExtractsTopResolvedFields(t *testing.T) {
	result := core.Result{
		CRFResult: core.CRFResult{
			Details: core.Details{Content: "77 RUE DE RIVOLI 75001 PARIS"},
			PredictionsPerTag: map[core.Tag][]core.PredictionCRF{
				core.TagPostalCode: {
					{Prediction: "75001", Confidence: 0.91},
				},
				core.TagStreet: {
					{Prediction: "RUE DE RIVOLI", Confidence: 0.88},
				},
			},
		},
		FuzzyResult: core.FuzzyResult{
			CountryMatches: []core.FuzzyMatch{
				{Origin: "FR", Possibility: "FR", FinalScore: 0.93},
			},
			TownMatches: []core.FuzzyMatch{
				{Origin: "FR", Possibility: "PARIS", FinalScore: 0.94},
			},
		},
	}

	got := structured.FromResult(result)

	if got.AddressLine != "77 RUE DE RIVOLI 75001 PARIS" {
		t.Fatalf("AddressLine = %q", got.AddressLine)
	}
	if got.Country != "FR" {
		t.Fatalf("Country = %q, want FR", got.Country)
	}
	if got.Town != "PARIS" {
		t.Fatalf("Town = %q, want PARIS", got.Town)
	}
	if got.PostalCode != "75001" {
		t.Fatalf("PostalCode = %q, want 75001", got.PostalCode)
	}
	if got.Street != "RUE DE RIVOLI" {
		t.Fatalf("Street = %q, want RUE DE RIVOLI", got.Street)
	}
	if got.CountryConfidence != 0.93 {
		t.Fatalf("CountryConfidence = %f, want 0.93", got.CountryConfidence)
	}
	if got.TownConfidence != 0.94 {
		t.Fatalf("TownConfidence = %f, want 0.94", got.TownConfidence)
	}
}

func TestFromResultIgnoresNoCountryAndNoTown(t *testing.T) {
	result := core.Result{
		CRFResult: core.CRFResult{Details: core.Details{Content: "UNKNOWN"}},
		FuzzyResult: core.FuzzyResult{
			CountryMatches: []core.FuzzyMatch{{Origin: "NO COUNTRY", Possibility: "NO COUNTRY", FinalScore: 0.15}},
			TownMatches:    []core.FuzzyMatch{{Origin: "NO TOWN", Possibility: "NO TOWN", FinalScore: 0.15}},
		},
	}

	got := structured.FromResult(result)

	if got.Country != "" {
		t.Fatalf("Country = %q, want empty", got.Country)
	}
	if got.Town != "" {
		t.Fatalf("Town = %q, want empty", got.Town)
	}
}
