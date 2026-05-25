package postprocess

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/resources"
)

func TestTownFlagManagerFlagsTownAloneOnLine(t *testing.T) {
	address := "Account holder\nParis\nFrance"
	towns := []core.FuzzyMatch{{
		Start:       15,
		End:         20,
		Matched:     "Paris",
		Possibility: "PARIS",
		Origin:      "FR",
	}}

	got := NewTownFlagManager(config.Default().PostProcessing, resources.Database{}).
		ApplyTownFlags(towns, address, core.CRFResult{})

	assertFlags(t, got[0].Flags, core.FlagIsAloneOnLine)
}

func TestTownFlagManagerDoesNotFlagTownWhenNotAloneOnLine(t *testing.T) {
	address := "City Paris\nFrance"
	towns := []core.FuzzyMatch{{
		Start:       5,
		End:         10,
		Matched:     "Paris",
		Possibility: "PARIS",
		Origin:      "FR",
	}}

	got := NewTownFlagManager(config.Default().PostProcessing, resources.Database{}).
		ApplyTownFlags(towns, address, core.CRFResult{})

	assertNotFlags(t, got[0].Flags, core.FlagIsAloneOnLine)
}

func TestTownFlagManagerFlagsSeparatorTypoAndPopulation(t *testing.T) {
	db := resources.Database{
		TownPopulations:    map[string]int{"NEW YORK": 8_000_000},
		LargestTownCountry: map[string]string{"NEW YORK": "US"},
	}
	towns := []core.FuzzyMatch{{
		Start:       0,
		End:         8,
		Matched:     "New-York",
		Possibility: "NEW YORK",
		Dist:        1,
		Origin:      "GB",
	}}

	got := NewTownFlagManager(config.Default().PostProcessing, db).
		ApplyTownFlags(towns, "New-York", core.CRFResult{})

	assertFlags(t, got[0].Flags,
		core.FlagIsSeparatorTypo,
		core.FlagIsMetropolis,
		core.FlagIsNotLargestTownWithName,
	)
}

func TestTownFlagManagerFlagsMatchInsideStreetAtConfiguredRatio(t *testing.T) {
	cfg := config.Default().PostProcessing
	cfg.PartOfStreetRatio = 0.50
	towns := []core.FuzzyMatch{{
		Start:       4,
		End:         10,
		Matched:     "Berlin",
		Possibility: "BERLIN",
		Origin:      "DE",
	}}
	crf := core.CRFResult{
		Details: core.Details{
			Spans: []core.TaggedSpan{{
				Start: 4,
				End:   7,
				Tag:   core.TagStreet,
			}},
		},
	}

	got := NewTownFlagManager(cfg, resources.Database{}).
		ApplyTownFlags(towns, "Rue Berlin", crf)

	assertFlags(t, got[0].Flags, core.FlagIsInsideStreet)
}

func TestCountryFlagManagerFlagsProvinceAliases(t *testing.T) {
	cfg := config.Default().PostProcessing
	db := resources.Database{
		Provinces: map[string][]string{
			"US": {"CA"},
			"IN": {"MH"},
		},
	}
	countries := []core.FuzzyMatch{
		{Start: 0, End: 2, Matched: "CA", Possibility: "CA", Origin: "US"},
		{Start: 10, End: 12, Matched: "MH", Possibility: "MH", Origin: "IN"},
	}

	got := NewCountryFlagManager(cfg, db).
		ApplyCountryFlags(countries, "CA MH", core.CRFResult{}, nil)

	assertFlags(t, got[0].Flags, core.FlagIsCommonStateProvinceAlias)
	assertFlags(t, got[1].Flags, core.FlagIsUncommonStateProvinceAlias)
}

func TestRelationshipFlagManagerFlagsMatchingOriginCountryAndTown(t *testing.T) {
	address := "Paris, France"
	towns := []core.FuzzyMatch{{
		Start:       0,
		End:         5,
		Matched:     "Paris",
		Possibility: "PARIS",
		Origin:      "FR",
	}}
	countries := []core.FuzzyMatch{{
		Start:       7,
		End:         13,
		Matched:     "France",
		Possibility: "FRANCE",
		Origin:      "FR",
	}}

	gotTowns, gotCountries := NewRelationshipFlagManager().
		AddRelationshipFlags(towns, countries, address, "")

	assertFlags(t, gotTowns[0].Flags,
		core.FlagCountryIsPresent,
		core.FlagIsVeryCloseToCountry,
		core.FlagIsOnSameLineAsCountry,
	)
	assertFlags(t, gotCountries[0].Flags,
		core.FlagTownIsPresent,
		core.FlagIsVeryCloseToTown,
		core.FlagIsOnSameLineAsTown,
	)
}

func TestRelationshipFlagManagerFlagsSameSpanCountryAndTown(t *testing.T) {
	address := "Singapore"
	towns := []core.FuzzyMatch{{
		Start:       0,
		End:         9,
		Matched:     "Singapore",
		Possibility: "SINGAPORE",
		Origin:      "SG",
	}}
	countries := []core.FuzzyMatch{{
		Start:       0,
		End:         9,
		Matched:     "Singapore",
		Possibility: "SINGAPORE",
		Origin:      "SG",
	}}

	gotTowns, gotCountries := NewRelationshipFlagManager().
		AddRelationshipFlags(towns, countries, address, "")

	assertFlags(t, gotTowns[0].Flags,
		core.FlagCountryIsPresent,
		core.FlagIsVeryCloseToCountry,
		core.FlagIsOnSameLineAsCountry,
	)
	assertFlags(t, gotCountries[0].Flags,
		core.FlagTownIsPresent,
		core.FlagIsVeryCloseToTown,
		core.FlagIsOnSameLineAsTown,
	)
}

func TestRelationshipFlagManagerCheckReasonableMistakesFlagsTownFromCRFCountryPrediction(t *testing.T) {
	towns := []core.FuzzyMatch{{
		Start:       0,
		End:         5,
		Matched:     "Paris",
		Possibility: "PARIS",
		Origin:      "FR",
	}}
	crf := core.CRFResult{
		PredictionsPerTag: map[core.Tag][]core.PredictionCRF{
			core.TagCountry: {{
				Prediction: "Paris France",
			}},
		},
	}

	gotTowns, _ := NewRelationshipFlagManager().
		CheckReasonableMistakes(towns, nil, crf)

	assertFlags(t, gotTowns[0].Flags, core.FlagCouldBeReasonableMistake)
}

func TestRelationshipFlagManagerCheckReasonableMistakesFlagsCountryFromCRFTownPrediction(t *testing.T) {
	countries := []core.FuzzyMatch{{
		Start:       7,
		End:         13,
		Matched:     "France",
		Possibility: "FRANCE",
		Origin:      "FR",
	}}
	crf := core.CRFResult{
		PredictionsPerTag: map[core.Tag][]core.PredictionCRF{
			core.TagTown: {{
				Prediction: "Ile de France",
			}},
		},
	}

	_, gotCountries := NewRelationshipFlagManager().
		CheckReasonableMistakes(nil, countries, crf)

	assertFlags(t, gotCountries[0].Flags, core.FlagCouldBeReasonableMistake)
}

func TestMatchInclusionFlaggerFlagsNestedMatchesByRank(t *testing.T) {
	matches := []core.FuzzyMatch{
		{Start: 2, End: 4, Matched: "IL", Possibility: "IL", Origin: "IL"},
		{Start: 0, End: 5, Matched: "CHILE", Possibility: "CHILE", Origin: "CL"},
	}

	got := MatchInclusionFlagger{}.FlagMatchesIncludedInAnother(matches, matches)

	assertFlags(t, got[0].Flags, core.FlagIsInsideAnotherLowerRankedMatch)
	assertNotFlags(t, got[1].Flags, core.FlagIsInsideAnotherLowerRankedMatch, core.FlagIsInsideAnotherHigherRankedMatch)
}

func assertFlags(t *testing.T, flags []core.Flag, want ...core.Flag) {
	t.Helper()

	for _, flag := range want {
		if !hasFlag(flags, flag) {
			t.Fatalf("flags = %#v, want to contain %q", flags, flag)
		}
	}
}

func assertNotFlags(t *testing.T, flags []core.Flag, unwanted ...core.Flag) {
	t.Helper()

	for _, flag := range unwanted {
		if hasFlag(flags, flag) {
			t.Fatalf("flags = %#v, did not want %q", flags, flag)
		}
	}
}

func hasFlag(flags []core.Flag, want core.Flag) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}
