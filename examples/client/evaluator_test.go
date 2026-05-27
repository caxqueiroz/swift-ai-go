package main

import (
	"math/rand/v2"
	"testing"
)

func TestCanonicalTownMatchesCaseAccentAndPunctuationVariants(t *testing.T) {
	left := canonicalTown("São-Paulo")
	right := canonicalTown(" sao paulo ")
	if left != right {
		t.Fatalf("canonical towns differ: %q != %q", left, right)
	}
}

func TestMetricRecorderTracksCountryTownAndBothAccuracy(t *testing.T) {
	recorder := newMetricRecorder()
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "SG",
		ActualTown:      "Singapore",
	})
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "MY",
		ActualTown:      "Singapore",
	})
	recorder.Add(evaluationRow{
		Stratum:         "FR:rare",
		ExpectedCountry: "FR",
		ExpectedTown:    "MONTREUIL",
		ActualCountry:   "FR",
		ActualTown:      "PARIS",
	})

	report := recorder.Report()
	if report.Total != 3 {
		t.Fatalf("total = %d, want 3", report.Total)
	}
	if report.CountryCorrect != 2 {
		t.Fatalf("country correct = %d, want 2", report.CountryCorrect)
	}
	if report.TownCorrect != 2 {
		t.Fatalf("town correct = %d, want 2", report.TownCorrect)
	}
	if report.BothCorrect != 1 {
		t.Fatalf("both correct = %d, want 1", report.BothCorrect)
	}
	if got := report.Strata["SG:common"].Total; got != 2 {
		t.Fatalf("SG stratum total = %d, want 2", got)
	}
}

func TestPlanSamplesAllocatesAcrossStrataDeterministically(t *testing.T) {
	groups := []townGroup{
		{Country: "SG", Town: "SINGAPORE", Count: 5000},
		{Country: "FR", Town: "PARIS", Count: 5000},
		{Country: "FR", Town: "MONTREUIL", Count: 50},
	}

	first := planSamples(groups, 6, rand.New(rand.NewPCG(1, 2)))
	second := planSamples(groups, 6, rand.New(rand.NewPCG(1, 2)))
	if len(first) != 6 {
		t.Fatalf("sample count = %d, want 6", len(first))
	}
	if len(second) != len(first) {
		t.Fatalf("second sample count = %d, want %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("plan not deterministic at %d: %#v != %#v", i, first[i], second[i])
		}
	}

	seen := map[string]bool{}
	for _, sample := range first {
		seen[sample.Stratum] = true
	}
	for _, stratum := range []string{"FR:common", "FR:rare", "SG:common"} {
		if !seen[stratum] {
			t.Fatalf("missing stratum %q in plan %#v", stratum, first)
		}
	}
}

func TestBuildInputTextModes(t *testing.T) {
	record := addressRecord{Address: "6 Hirtenfeldbergstrasse", Town: "Kumberg", Country: "AT"}

	tests := []struct {
		mode string
		want string
	}{
		{mode: inputModeAddress, want: "6 Hirtenfeldbergstrasse"},
		{mode: inputModeAddressTown, want: "6 Hirtenfeldbergstrasse\nKumberg"},
		{mode: inputModeAddressTownCountry, want: "6 Hirtenfeldbergstrasse\nKumberg\nAT"},
		{mode: "unknown", want: "6 Hirtenfeldbergstrasse\nKumberg"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := buildInputText(record, tt.mode); got != tt.want {
				t.Fatalf("buildInputText() = %q, want %q", got, tt.want)
			}
		})
	}
}
