package judge_test

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/judge"
)

func TestValidateDecisionAcceptsOnlyGeoNamesBackedCandidates(t *testing.T) {
	req := judge.Request{
		Countries: []judge.CountryCandidate{{Code: "FR"}, {Code: "DE"}},
		Towns: []judge.TownCandidate{
			{Name: "PARIS", CountryCode: "FR"},
			{Name: "BERLIN", CountryCode: "DE"},
		},
	}
	decision := judge.Decision{Resolved: true, Country: "FR", Town: "PARIS"}

	got, ok := judge.ValidateDecision(req, decision)

	if !ok {
		t.Fatal("ValidateDecision rejected GeoNames-backed decision")
	}
	if got.Country != "FR" || got.Town != "PARIS" {
		t.Fatalf("decision = %#v, want FR/PARIS", got)
	}
}

func TestValidateDecisionRejectsInventedTown(t *testing.T) {
	req := judge.Request{
		Countries: []judge.CountryCandidate{{Code: "FR"}},
		Towns:     []judge.TownCandidate{{Name: "PARIS", CountryCode: "FR"}},
	}
	decision := judge.Decision{Resolved: true, Country: "FR", Town: "LYON"}

	_, ok := judge.ValidateDecision(req, decision)

	if ok {
		t.Fatal("ValidateDecision accepted invented town")
	}
}

func TestValidateDecisionRejectsMismatchedTownCountry(t *testing.T) {
	req := judge.Request{
		Countries: []judge.CountryCandidate{{Code: "FR"}, {Code: "DE"}},
		Towns: []judge.TownCandidate{
			{Name: "PARIS", CountryCode: "FR"},
			{Name: "BERLIN", CountryCode: "DE"},
		},
	}
	decision := judge.Decision{Resolved: true, Country: "FR", Town: "BERLIN"}

	_, ok := judge.ValidateDecision(req, decision)

	if ok {
		t.Fatal("ValidateDecision accepted town from different country")
	}
}
