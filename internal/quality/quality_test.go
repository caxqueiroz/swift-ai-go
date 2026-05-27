package quality_test

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/quality"
	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestAssessMarksHighConfidenceCountryAndTownResolved(t *testing.T) {
	got := quality.Assess(structured.Address{
		Country:           "FR",
		Town:              "PARIS",
		CountryConfidence: 0.97,
		TownConfidence:    0.96,
	}, quality.DefaultThresholds())

	if got.Status != quality.StatusResolved {
		t.Fatalf("Status = %q, want %q", got.Status, quality.StatusResolved)
	}
	if got.Band != quality.BandHigh {
		t.Fatalf("Band = %q, want %q", got.Band, quality.BandHigh)
	}
	if got.Score != 0.96 {
		t.Fatalf("Score = %f, want 0.96", got.Score)
	}
}

func TestAssessMarksMediumConfidenceCompleteAddressNeedsReview(t *testing.T) {
	got := quality.Assess(structured.Address{
		Country:           "FR",
		Town:              "PARIS",
		CountryConfidence: 0.91,
		TownConfidence:    0.88,
	}, quality.DefaultThresholds())

	if got.Status != quality.StatusNeedsReview {
		t.Fatalf("Status = %q, want %q", got.Status, quality.StatusNeedsReview)
	}
	if got.Band != quality.BandMedium {
		t.Fatalf("Band = %q, want %q", got.Band, quality.BandMedium)
	}
	if got.Reason != quality.ReasonConfidenceBelowHigh {
		t.Fatalf("Reason = %q, want %q", got.Reason, quality.ReasonConfidenceBelowHigh)
	}
}

func TestAssessMarksMissingTownPartial(t *testing.T) {
	got := quality.Assess(structured.Address{
		Country:           "FR",
		CountryConfidence: 0.93,
	}, quality.DefaultThresholds())

	if got.Status != quality.StatusPartial {
		t.Fatalf("Status = %q, want %q", got.Status, quality.StatusPartial)
	}
	if got.Reason != quality.ReasonMissingTown {
		t.Fatalf("Reason = %q, want %q", got.Reason, quality.ReasonMissingTown)
	}
}

func TestAssessMarksLowConfidenceNeedsReview(t *testing.T) {
	got := quality.Assess(structured.Address{
		Country:           "FR",
		Town:              "PARIS",
		CountryConfidence: 0.72,
		TownConfidence:    0.70,
	}, quality.DefaultThresholds())

	if got.Status != quality.StatusNeedsReview {
		t.Fatalf("Status = %q, want %q", got.Status, quality.StatusNeedsReview)
	}
	if got.Band != quality.BandLow {
		t.Fatalf("Band = %q, want %q", got.Band, quality.BandLow)
	}
}
