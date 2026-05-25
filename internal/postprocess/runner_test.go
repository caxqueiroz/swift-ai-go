package postprocess

import (
	"math"
	"slices"
	"testing"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/resources"
)

func TestRunnerSuggestedCountryCreatesGeneratedCountryMatch(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	crf := runnerTestCRF("Beneficiary address", nil, nil)
	sample := core.AddressSample{
		Text:                "Beneficiary address",
		SuggestedCountry:    "JP",
		HasSuggestedCountry: true,
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{{}},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{sample},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	country := got[0].FuzzyResult.CountryMatches[0]
	if country.Origin != "JP" || country.Possibility != "JP" {
		t.Fatalf("top country = %#v, want generated JP country", country)
	}
	assertFlags(t, country.Flags, core.FlagGeneratedBySuggestedCountry)
}

func TestRunnerSuggestedCountryCreatesGeneratedMatchWhenCountryAlreadyExists(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	text := "Japan"
	crf := runnerTestCRF(text, fillSeries(len(text), 0.5), nil)
	fuzzy := core.FuzzyResult{
		CountryMatches: []core.FuzzyMatch{{
			Start:       0,
			End:         5,
			Matched:     "Japan",
			Possibility: "JAPAN",
			Origin:      "JP",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{
			Text:                text,
			SuggestedCountry:    "JP",
			HasSuggestedCountry: true,
		}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !hasCountryWithFlag(got[0].FuzzyResult.CountryMatches, "JP", core.FlagGeneratedBySuggestedCountry) {
		t.Fatalf("country matches = %#v, want generated suggested JP match even with existing JP country", got[0].FuzzyResult.CountryMatches)
	}
}

func TestRunnerSuggestedCountryCodeMatchIsFlaggedAndRaised(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	text := "JP"
	crf := runnerTestCRF(text, zeroSeries(len(text)), nil)
	fuzzy := core.FuzzyResult{
		CountryCodeMatches: []core.FuzzyMatch{{
			Start:       0,
			End:         2,
			Matched:     "JP",
			Possibility: "JP",
			Origin:      "JP",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{
			Text:                text,
			SuggestedCountry:    "JP",
			HasSuggestedCountry: true,
		}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	code := findCountryByPossibility(got[0].FuzzyResult.CountryCodeMatches, "JP")
	if code == nil {
		t.Fatalf("country code matches = %#v, want JP", got[0].FuzzyResult.CountryCodeMatches)
	}
	assertFlags(t, code.Flags, core.FlagIsSuggestedCountry)
	if code.CRFScore != cfg.PostProcessing.BaseScoreSuggestedCountry {
		t.Fatalf("suggested country code CRFScore = %f, want base score %f", code.CRFScore, cfg.PostProcessing.BaseScoreSuggestedCountry)
	}

	generated := findCountryByFlag(got[0].FuzzyResult.CountryMatches, core.FlagGeneratedBySuggestedCountry)
	if generated == nil {
		t.Fatalf("country matches = %#v, want generated suggested country", got[0].FuzzyResult.CountryMatches)
	}
	if generated.FinalScore != cfg.PostProcessing.BaseScoreSuggestedCountry {
		t.Fatalf("generated suggested country FinalScore = %f, want base score %f", generated.FinalScore, cfg.PostProcessing.BaseScoreSuggestedCountry)
	}
}

func TestRunnerFiltersZeroScoreCountryCodesUnlessCountryHeadAgrees(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	text := "FR DE"
	crf := runnerTestCRF(text, zeroSeries(len(text)), nil)
	crf.Details.CountryCode = "FR"
	fuzzy := core.FuzzyResult{
		CountryCodeMatches: []core.FuzzyMatch{
			{Start: 0, End: 2, Matched: "FR", Possibility: "FR", Origin: "FR"},
			{Start: 3, End: 5, Matched: "DE", Possibility: "DE", Origin: "DE"},
		},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	codeMatches := got[0].FuzzyResult.CountryCodeMatches
	if len(codeMatches) != 1 || codeMatches[0].Origin != "FR" {
		t.Fatalf("country code matches = %#v, want only FR preserved by country head", codeMatches)
	}
}

func TestRunnerCountryHeadConfidenceControlsTownMLPFlag(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		wantFlag   bool
	}{
		{
			name:       "low confidence",
			confidence: 0.50,
			wantFlag:   false,
		},
		{
			name:       "high confidence",
			confidence: 0.99,
			wantFlag:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := runnerTestConfig()
			runner := NewRunner(cfg, resources.Database{})
			text := "Paris"
			crf := runnerTestCRF(text, nil, fillSeries(len(text), 0.9))
			crf.Details.CountryCode = "FR"
			crf.Details.CountryCodeConfidence = tt.confidence
			fuzzy := core.FuzzyResult{
				TownMatches: []core.FuzzyMatch{{
					Start:       0,
					End:         5,
					Matched:     "Paris",
					Possibility: "PARIS",
					Origin:      "FR",
				}},
			}

			got, err := runner.Run(
				[]core.CRFResult{crf},
				[]core.FuzzyResult{fuzzy},
				[][]core.PostcodeMatch{nil},
				[]core.AddressSample{{Text: text}},
			)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			town := findTownByPossibility(got[0].FuzzyResult.TownMatches, "PARIS")
			if town == nil {
				t.Fatalf("town matches = %#v, want PARIS", got[0].FuzzyResult.TownMatches)
			}
			if tt.wantFlag {
				assertFlags(t, town.Flags, core.FlagMLPCountryIsPresent)
				return
			}
			assertNotFlags(t, town.Flags, core.FlagMLPCountryIsPresent)
		})
	}
}

func TestRunnerCountryCodeInclusionUsesOriginalSortedRank(t *testing.T) {
	cfg := runnerTestConfig()
	cfg.PostProcessing.MinimalFinalScoreCountry = 0
	runner := NewRunner(cfg, resources.Database{})
	text := "CHILE"
	crf := runnerTestCRF(text, fillSeries(len(text), 0.9), nil)
	fuzzy := core.FuzzyResult{
		CountryMatches: []core.FuzzyMatch{{
			Start:       0,
			End:         5,
			Matched:     "CHILE",
			Possibility: "CHILE",
			Origin:      "CL",
		}},
		CountryCodeMatches: []core.FuzzyMatch{{
			Start:       2,
			End:         4,
			Matched:     "IL",
			Possibility: "IL",
			Origin:      "IL",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	countryCode := findCountryByPossibility(got[0].FuzzyResult.CountryMatches, "IL")
	if countryCode == nil {
		t.Fatalf("country matches = %#v, want nested IL country code", got[0].FuzzyResult.CountryMatches)
	}
	assertFlags(t, countryCode.Flags, core.FlagIsInsideAnotherHigherRankedMatch)
	assertNotFlags(t, countryCode.Flags, core.FlagIsInsideAnotherLowerRankedMatch)
}

func TestRunnerCountryCodeInclusionPreservesLowerRankedPath(t *testing.T) {
	cfg := runnerTestConfig()
	cfg.PostProcessing.MinimalFinalScoreCountry = 0
	runner := NewRunner(cfg, resources.Database{})
	text := "CHILE"
	countryScores := fillSeries(len(text), 0.8)
	setRange(countryScores, 2, 4, 0.95)
	crf := runnerTestCRF(text, countryScores, nil)
	fuzzy := core.FuzzyResult{
		CountryMatches: []core.FuzzyMatch{{
			Start:       0,
			End:         5,
			Matched:     "CHILE",
			Possibility: "CHILE",
			Origin:      "CL",
		}},
		CountryCodeMatches: []core.FuzzyMatch{{
			Start:       2,
			End:         4,
			Matched:     "IL",
			Possibility: "IL",
			Origin:      "IL",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	countryCode := findCountryByPossibility(got[0].FuzzyResult.CountryMatches, "IL")
	if countryCode == nil {
		t.Fatalf("country matches = %#v, want nested IL country code", got[0].FuzzyResult.CountryMatches)
	}
	assertFlags(t, countryCode.Flags, core.FlagIsInsideAnotherLowerRankedMatch)
	assertNotFlags(t, countryCode.Flags, core.FlagIsInsideAnotherHigherRankedMatch)
}

func TestRunnerFlagsExtendedTownMatches(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	text := "Monaco"
	crf := runnerTestCRF(text, nil, fillSeries(len(text), 0.9))
	fuzzy := core.FuzzyResult{
		ExtendedTownMatches: []core.FuzzyMatch{{
			Start:       0,
			End:         6,
			Matched:     "Monaco",
			Possibility: "MONACO",
			Origin:      "MC",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	town := findTownByPossibility(got[0].FuzzyResult.TownMatches, "MONACO")
	if town == nil {
		t.Fatalf("town matches = %#v, want MONACO", got[0].FuzzyResult.TownMatches)
	}
	assertFlags(t, town.Flags, core.FlagIsFromExtendedData)
}

func TestRunnerScoresMultibyteByteSpanUsingRuneCRFIndexes(t *testing.T) {
	cfg := runnerTestConfig()
	cfg.PostProcessing.MinimalFinalScoreTown = 0
	runner := NewRunner(cfg, resources.Database{})
	text := "é Paris"
	townScores := []float64{0.1, 0.1, 0.9, 0.8, 0.7, 0.6, 0.5}
	crf := runnerTestCRF(text, fillSeries(len([]rune(text)), 0.5), townScores)
	start := len("é ")
	end := len(text)
	fuzzy := core.FuzzyResult{
		TownMatches: []core.FuzzyMatch{{
			Start:       start,
			End:         end,
			Matched:     text[start:end],
			Possibility: "PARIS",
			Origin:      "FR",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	town := findTownByPossibility(got[0].FuzzyResult.TownMatches, "PARIS")
	if town == nil {
		t.Fatalf("town matches = %#v, want PARIS", got[0].FuzzyResult.TownMatches)
	}
	want := 0.7
	if math.Abs(town.CRFScore-want) > 1e-9 {
		t.Fatalf("town CRFScore = %f, want %f from rune span", town.CRFScore, want)
	}
	if town.Start != start || town.End != end {
		t.Fatalf("town span = [%d,%d], want original byte span [%d,%d]", town.Start, town.End, start, end)
	}
}

func TestRunnerConvertsCRFStreetSpansToByteOffsetsForFlags(t *testing.T) {
	cfg := runnerTestConfig()
	cfg.PostProcessing.MinimalFinalScoreTown = 0
	cfg.PostProcessing.PartOfStreetRatio = 1.0
	runner := NewRunner(cfg, resources.Database{})
	text := "é Main"
	townScores := []float64{0.1, 0.1, 0.9, 0.9, 0.9, 0.9}
	crf := runnerTestCRF(text, fillSeries(len([]rune(text)), 0.5), townScores)
	crf.Details.Spans = []core.TaggedSpan{{
		Start: 2,
		End:   6,
		Tag:   core.TagStreet,
	}}
	start := len("é ")
	end := len(text)
	fuzzy := core.FuzzyResult{
		TownMatches: []core.FuzzyMatch{{
			Start:       start,
			End:         end,
			Matched:     text[start:end],
			Possibility: "MAIN",
			Origin:      "FR",
		}},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	town := findTownByPossibility(got[0].FuzzyResult.TownMatches, "MAIN")
	if town == nil {
		t.Fatalf("town matches = %#v, want MAIN", got[0].FuzzyResult.TownMatches)
	}
	assertFlags(t, town.Flags, core.FlagIsInsideStreet)
	if town.Start != start || town.End != end {
		t.Fatalf("town span = [%d,%d], want original byte span [%d,%d]", town.Start, town.End, start, end)
	}
	gotSpan := got[0].CRFResult.Details.Spans[0]
	if gotSpan.Start != 2 || gotSpan.End != 6 {
		t.Fatalf("result CRF span = [%d,%d], want original rune span [2,6]", gotSpan.Start, gotSpan.End)
	}
}

func TestRunnerOrdersFinalMatchesByCombinationScore(t *testing.T) {
	cfg := runnerTestConfig()
	runner := NewRunner(cfg, resources.Database{})
	text := "Paris France Berlin Germany"
	countryScores := fillSeries(len(text), 0.1)
	townScores := fillSeries(len(text), 0.1)
	setRange(countryScores, 6, 12, 0.7)
	setRange(townScores, 0, 5, 0.7)
	setRange(countryScores, 20, 27, 0.95)
	setRange(townScores, 13, 19, 0.95)
	crf := runnerTestCRF(text, countryScores, townScores)
	fuzzy := core.FuzzyResult{
		CountryMatches: []core.FuzzyMatch{
			{Start: 6, End: 12, Matched: "France", Possibility: "FRANCE", Origin: "FR"},
			{Start: 20, End: 27, Matched: "Germany", Possibility: "GERMANY", Origin: "DE"},
		},
		TownMatches: []core.FuzzyMatch{
			{Start: 0, End: 5, Matched: "Paris", Possibility: "PARIS", Origin: "FR"},
			{Start: 13, End: 19, Matched: "Berlin", Possibility: "BERLIN", Origin: "DE"},
		},
	}

	got, err := runner.Run(
		[]core.CRFResult{crf},
		[]core.FuzzyResult{fuzzy},
		[][]core.PostcodeMatch{nil},
		[]core.AddressSample{{Text: text}},
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	countries := got[0].FuzzyResult.CountryMatches
	towns := got[0].FuzzyResult.TownMatches
	if len(countries) < 2 || len(towns) < 2 {
		t.Fatalf("final matches = countries %#v towns %#v, want at least two combinations", countries, towns)
	}
	if countries[0].Origin != "DE" || towns[0].Possibility != "BERLIN" {
		t.Fatalf("top combination = (%s, %s), want (DE, BERLIN)", countries[0].Origin, towns[0].Possibility)
	}
	if countries[1].Origin != "FR" || towns[1].Possibility != "PARIS" {
		t.Fatalf("second combination = (%s, %s), want (FR, PARIS)", countries[1].Origin, towns[1].Possibility)
	}
}

func runnerTestConfig() config.Config {
	cfg := config.Default()
	cfg.PostProcessing.MinimalFinalScoreCountry = 0.01
	cfg.PostProcessing.MinimalFinalScoreTown = 0.01
	return cfg
}

func runnerTestCRF(text string, countryScores []float64, townScores []float64) core.CRFResult {
	if countryScores == nil {
		countryScores = fillSeries(len(text), 0.5)
	}
	if townScores == nil {
		townScores = fillSeries(len(text), 0.5)
	}

	return core.CRFResult{
		Details: core.Details{
			Content: text,
		},
		PredictionsPerTag: map[core.Tag][]core.PredictionCRF{},
		EmissionsPerTag: map[core.Tag][]float64{
			core.TagCountry: slices.Clone(countryScores),
			core.TagTown:    slices.Clone(townScores),
		},
		LogProbasPerTag: map[core.Tag][]float64{
			core.TagCountry: slices.Clone(countryScores),
			core.TagTown:    slices.Clone(townScores),
		},
	}
}

func zeroSeries(length int) []float64 {
	return make([]float64, length)
}

func fillSeries(length int, value float64) []float64 {
	values := make([]float64, length)
	for i := range values {
		values[i] = value
	}
	return values
}

func setRange(values []float64, start int, end int, value float64) {
	for i := start; i < end; i++ {
		values[i] = value
	}
}

func findTownByPossibility(matches []core.FuzzyMatch, possibility string) *core.FuzzyMatch {
	for i := range matches {
		if matches[i].Possibility == possibility {
			return &matches[i]
		}
	}
	return nil
}

func findCountryByPossibility(matches []core.FuzzyMatch, possibility string) *core.FuzzyMatch {
	for i := range matches {
		if matches[i].Possibility == possibility {
			return &matches[i]
		}
	}
	return nil
}

func hasCountryWithFlag(matches []core.FuzzyMatch, origin string, flag core.Flag) bool {
	for _, match := range matches {
		if match.Origin == origin && hasFlag(match.Flags, flag) {
			return true
		}
	}
	return false
}

func findCountryByFlag(matches []core.FuzzyMatch, flag core.Flag) *core.FuzzyMatch {
	for i := range matches {
		if hasFlag(matches[i].Flags, flag) {
			return &matches[i]
		}
	}
	return nil
}
