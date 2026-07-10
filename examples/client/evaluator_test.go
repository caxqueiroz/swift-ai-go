package main

import (
	"io"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/tipmarket/swift-ai/internal/holdout"
)

func TestCanonicalTownMatchesCaseAccentAndPunctuationVariants(t *testing.T) {
	left := canonicalTown("São-Paulo")
	right := canonicalTown(" sao paulo ")
	if left != right {
		t.Fatalf("canonical towns differ: %q != %q", left, right)
	}
}

func TestCanonicalTownMatchesTransliteratedVariants(t *testing.T) {
	tests := []struct {
		name   string
		left   string
		right  string
		reason string
	}{
		{
			name:  "arabic",
			left:  "العين",
			right: "L'YN",
		},
		{
			name:  "chinese latin case",
			left:  "Wuhan",
			right: "WUHAN",
		},
		{
			name:  "japanese",
			left:  "綾瀬市",
			right: "LING LAI SHI",
		},
		{
			name:  "korean",
			left:  "금정구",
			right: "GEUMJEONGGU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := canonicalTown(tt.left)
			right := canonicalTown(tt.right)
			if left != right {
				t.Fatalf("canonical towns differ: %q -> %q, %q -> %q", tt.left, left, tt.right, right)
			}
		})
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

func TestMetricRecorderTracksCommittedCoverageAndPrecision(t *testing.T) {
	recorder := newMetricRecorder()
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "SG",
		ActualTown:      "Singapore",
		Status:          "resolved",
		Band:            "high",
	})
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "SG",
		ActualTown:      "QUEENSTOWN",
		Status:          "resolved",
		Band:            "high",
	})
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "SG",
		ActualTown:      "Singapore",
		Status:          "needs_review",
		Band:            "medium",
	})
	recorder.Add(evaluationRow{
		Stratum:         "SG:common",
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "MY",
		ActualTown:      "Singapore",
		Status:          "resolved",
		Band:            "high",
	})

	report := recorder.Report()
	if report.Committed != 3 {
		t.Fatalf("committed = %d, want 3", report.Committed)
	}
	if report.CommittedBothCorrect != 1 {
		t.Fatalf("committed both correct = %d, want 1", report.CommittedBothCorrect)
	}
	if report.CommittedCountryCorrect != 2 {
		t.Fatalf("committed country correct = %d, want 2", report.CommittedCountryCorrect)
	}
	assertFloatNear(t, "coverage", report.Coverage, 0.75)
	assertFloatNear(t, "precision_at_committed_both", report.PrecisionAtCommittedBoth, 1.0/3.0)
	assertFloatNear(t, "precision_at_committed_country", report.PrecisionAtCommittedCountry, 2.0/3.0)

	stratum := report.Strata["SG:common"]
	if stratum.Committed != 3 {
		t.Fatalf("stratum committed = %d, want 3", stratum.Committed)
	}
}

func TestShouldLogRowHonorsLogAll(t *testing.T) {
	match := evaluationRow{
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "SG",
		ActualTown:      "Singapore",
	}
	mismatch := evaluationRow{
		ExpectedCountry: "SG",
		ExpectedTown:    "SINGAPORE",
		ActualCountry:   "MY",
		ActualTown:      "Singapore",
	}

	if shouldLogRow(match, false) {
		t.Fatal("matched row logged without log-all")
	}
	if !shouldLogRow(mismatch, false) {
		t.Fatal("mismatch row not logged without log-all")
	}
	if !shouldLogRow(match, true) {
		t.Fatal("matched row not logged with log-all")
	}
}

func TestHoldoutFilterUsesStableIDBuckets(t *testing.T) {
	filter := holdout.Filter{Mod: 10, MinBucket: 8}
	if !filter.Enabled() {
		t.Fatal("filter should be enabled")
	}

	for _, id := range []string{"addr-2", "addr-7", "addr-11"} {
		bucket := holdout.StableBucket(id, filter.Mod)
		if bucket < filter.MinBucket && filter.Include(id) {
			t.Fatalf("included id %q in bucket %d below min bucket %d", id, bucket, filter.MinBucket)
		}
		if bucket >= filter.MinBucket && !filter.Include(id) {
			t.Fatalf("excluded id %q in bucket %d at or above min bucket %d", id, bucket, filter.MinBucket)
		}
	}
	if filter.Include("") {
		t.Fatal("empty id should not pass enabled holdout filter")
	}
}

func TestParseArgsAllowsNoOverallTimeoutWithRequestTimeout(t *testing.T) {
	opts, err := parseArgs([]string{
		"--timeout", "0",
		"--request-timeout", "10m",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.timeout != 0 {
		t.Fatalf("timeout = %s, want 0", opts.timeout)
	}
	if opts.requestTimeout != 10*time.Minute {
		t.Fatalf("request timeout = %s, want 10m", opts.requestTimeout)
	}
}

func TestParseArgsDefaultsAPIURLToIPv4Loopback(t *testing.T) {
	opts, err := parseArgs(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.apiURL != "http://127.0.0.1:8080/convert" {
		t.Fatalf("apiURL = %q, want IPv4 loopback", opts.apiURL)
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

func assertFloatNear(t *testing.T, name string, got float64, want float64) {
	t.Helper()

	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("%s = %f, want %f", name, got, want)
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
