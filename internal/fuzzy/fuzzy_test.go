package fuzzy

import (
	"reflect"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

func TestScanAllBatchedCountryCodeMatchesOnlyWholeToken(t *testing.T) {
	got := ScanAllBatched(
		[]string{"SHIP FROM FR VIA FROST"},
		map[string][]string{"fr": {"FR"}},
		50,
		1,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       10,
			End:         12,
			Matched:     "FR",
			Possibility: "FR",
			Dist:        0,
			Origin:      "FR",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedNearMatchWithinTolerance(t *testing.T) {
	got := ScanAllBatched(
		[]string{"DESTINATION PAR1S"},
		map[string][]string{"PARIS": {"FR"}},
		50,
		1,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       12,
			End:         17,
			Matched:     "PAR1S",
			Possibility: "PARIS",
			Dist:        1,
			Origin:      "FR",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedMatchInsideAnotherWordIsFlagged(t *testing.T) {
	got := ScanAllBatched(
		[]string{"MILITARY BASE"},
		map[string][]string{"MILI": {"XX"}},
		50,
		0,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       0,
			End:         4,
			Matched:     "MILI",
			Possibility: "MILI",
			Dist:        0,
			Flags:       []core.Flag{core.FlagIsInsideAnotherWord},
			Origin:      "XX",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedMiddleNewlineReducesDistance(t *testing.T) {
	got := ScanAllBatched(
		[]string{"PAR\nIS"},
		map[string][]string{"PARIS": {"FR"}},
		50,
		1,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       0,
			End:         6,
			Matched:     "PAR\nIS",
			Possibility: "PARIS",
			Dist:        0,
			Origin:      "FR",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedExpandsMultipleOrigins(t *testing.T) {
	got := ScanAllBatched(
		[]string{"SPRINGFIELD"},
		map[string][]string{"SPRINGFIELD": {"US", "CA"}},
		50,
		1,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       0,
			End:         11,
			Matched:     "SPRINGFIELD",
			Possibility: "SPRINGFIELD",
			Dist:        0,
			Origin:      "CA",
		},
		{
			Start:       0,
			End:         11,
			Matched:     "SPRINGFIELD",
			Possibility: "SPRINGFIELD",
			Dist:        0,
			Origin:      "US",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedMergesCaseVariantKeysAndDedupesOrigins(t *testing.T) {
	got := ScanAllBatched(
		[]string{"PARIS"},
		map[string][]string{
			"paris": {"FR", "FR", "EU"},
			"PARIS": {"FR"},
		},
		50,
		1,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       0,
			End:         5,
			Matched:     "PARIS",
			Possibility: "PARIS",
			Dist:        0,
			Origin:      "EU",
		},
		{
			Start:       0,
			End:         5,
			Matched:     "PARIS",
			Possibility: "PARIS",
			Dist:        0,
			Origin:      "FR",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedSkipsEmptyCandidate(t *testing.T) {
	got := ScanAllBatched(
		[]string{"ABC"},
		map[string][]string{"": {"EMPTY"}},
		0,
		1,
	)

	want := [][]core.FuzzyMatch{{}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}

func TestScanAllBatchedPrefilterAllowsMaxDistanceLengthWindow(t *testing.T) {
	got := ScanAllBatched(
		[]string{"ABCDEF"},
		map[string][]string{"ABCDEFGHIJ": {"XX"}},
		50,
		4,
	)

	want := [][]core.FuzzyMatch{{
		{
			Start:       0,
			End:         6,
			Matched:     "ABCDEF",
			Possibility: "ABCDEFGHIJ",
			Dist:        4,
			Origin:      "XX",
		},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanAllBatched() = %#v, want %#v", got, want)
	}
}
