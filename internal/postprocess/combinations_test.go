package postprocess

import (
	"math"
	"testing"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
)

func TestCombinationMatchingCountryTownPairsAreGeneratedAndSorted(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	countries := []core.FuzzyMatch{
		createFuzzyMatch("France", "France", "FR", 0, 6, 0.82, nil),
		createFuzzyMatch("Germany", "Germany", "DE", 20, 27, 0.94, nil),
	}
	towns := []core.FuzzyMatch{
		createFuzzyMatch("Paris", "Paris", "FR", 8, 13, 0.84, nil),
		createFuzzyMatch("Berlin", "Berlin", "DE", 29, 35, 0.90, nil),
	}

	got := generator.Generate(countries, towns, noCountry, noTown)

	if len(got) != 6 {
		t.Fatalf("Generate returned %d combinations, want 6", len(got))
	}
	assertCombination(t, got[0], "DE", "Berlin", 0.92)
	assertCombination(t, got[1], "FR", "Paris", 0.83)
	assertSortedByScore(t, got)
}

func TestCombinationSoloCountriesUseNoTown(t *testing.T) {
	cfg := config.Default()
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, cfg.PostProcessing.MinimalFinalScoreCountry, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, cfg.PostProcessing.MinimalFinalScoreTown, nil)
	country := createFuzzyMatch("France", "France", "FR", 0, 6, 0.80, []core.Flag{
		core.FlagTownIsPresent,
		core.FlagIsVeryCloseToTown,
		core.FlagIsOnSameLineAsTown,
	})

	got := generator.Generate([]core.FuzzyMatch{country}, nil, noCountry, noTown)

	if len(got) != 1 {
		t.Fatalf("Generate returned %d combinations, want 1", len(got))
	}
	wantScore := (country.FinalScore + cfg.PostProcessing.MinimalFinalScoreTown -
		cfg.PostProcessing.NoTownFoundMul*(cfg.CountryWeights.TownIsPresent+
			cfg.CountryWeights.IsVeryCloseToTown+
			cfg.CountryWeights.IsOnSameLineAsTown)) / 2
	assertCombination(t, got[0], "FR", "NO TOWN", wantScore)
}

func TestCombinationSoloTownsUseNoCountry(t *testing.T) {
	cfg := config.Default()
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, cfg.PostProcessing.MinimalFinalScoreCountry, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, cfg.PostProcessing.MinimalFinalScoreTown, nil)
	town := createFuzzyMatch("Paris", "Paris", "FR", 8, 13, 0.80, []core.Flag{
		core.FlagCountryIsPresent,
		core.FlagSuggestedCountryIsPresent,
		core.FlagIsVeryCloseToCountry,
		core.FlagIsOnSameLineAsCountry,
	})

	got := generator.Generate(nil, []core.FuzzyMatch{town}, noCountry, noTown)

	if len(got) != 1 {
		t.Fatalf("Generate returned %d combinations, want 1", len(got))
	}
	wantScore := (cfg.PostProcessing.MinimalFinalScoreCountry + town.FinalScore -
		cfg.PostProcessing.NoCountryFoundMul*(cfg.TownWeights.CountryIsPresentBonus+
			cfg.TownWeights.SuggestedCountryIsPresentBonus+
			cfg.TownWeights.IsVeryCloseToCountry+
			cfg.TownWeights.IsOnSameLineAsCountry)) / 2
	assertCombination(t, got[0], "NO COUNTRY", "Paris", wantScore)
}

func TestCombinationNoMatchesReturnsNoCountryNoTown(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)

	got := generator.Generate(nil, nil, noCountry, noTown)

	if len(got) != 1 {
		t.Fatalf("Generate returned %d combinations, want 1", len(got))
	}
	assertCombination(t, got[0], "NO COUNTRY", "NO TOWN", 0.15)
}

func TestCombinationForcedSuggestedCountryFiltersMismatchedCountries(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	generator.SetSuggestedCountry("FR", true)
	generator.SetForceSuggestedCountry(true)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	countries := []core.FuzzyMatch{
		createFuzzyMatch("United States", "United States", "US", 0, 13, 0.95, nil),
		createFuzzyMatch("France", "France", "FR", 20, 26, 0.90, nil),
	}
	towns := []core.FuzzyMatch{
		createFuzzyMatch("New York", "New York", "US", 15, 23, 0.94, nil),
	}

	got := generator.Generate(countries, towns, noCountry, noTown)

	if !hasPair(got, "FR", "NO TOWN") {
		t.Fatalf("Generate did not keep forced suggested country FR as a solo country: %#v", got)
	}
	for _, combo := range got {
		if combo.Country.Origin == "US" {
			t.Fatalf("Generate included forced-out country %#v in combinations %#v", combo.Country, got)
		}
		if combo.Country.Origin == "NO COUNTRY" && combo.Town.Origin != "NO TOWN" {
			t.Fatalf("Generate included solo town while suggested country is forced: %#v", combo)
		}
	}
}

func TestCombinationForcedSuggestedNoCountryFiltersCountriesButKeepsSoloTowns(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	generator.SetSuggestedCountry("NO COUNTRY", true)
	generator.SetForceSuggestedCountry(true)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	country := createFuzzyMatch("France", "France", "FR", 7, 13, 0.95, nil)
	town := createFuzzyMatch("Paris", "Paris", "FR", 0, 5, 0.90, nil)

	got := generator.Generate([]core.FuzzyMatch{country}, []core.FuzzyMatch{town}, noCountry, noTown)

	if !hasPair(got, "NO COUNTRY", "Paris") {
		t.Fatalf("Generate did not keep solo town under forced NO COUNTRY: %#v", got)
	}
	for _, combo := range got {
		if combo.Country.Origin == "FR" {
			t.Fatalf("Generate included real country under forced NO COUNTRY: %#v in %#v", combo, got)
		}
	}
}

func TestCombinationSamePositionCountryTownPairsAreSkippedUnlessSameName(t *testing.T) {
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	country := createFuzzyMatch("Singapore", "Singapore", "SG", 0, 9, 0.95, nil)
	town := createFuzzyMatch("Singapore", "Singapore", "SG", 0, 9, 0.93, nil)

	withoutException := newDefaultCombinationGenerator(nil).Generate(
		[]core.FuzzyMatch{country},
		[]core.FuzzyMatch{town},
		noCountry,
		noTown,
	)
	if hasPair(withoutException, "SG", "Singapore") {
		t.Fatalf("Generate included same-position pair without exception: %#v", withoutException)
	}

	withException := newDefaultCombinationGenerator(map[string]string{"Singapore": "SG"}).Generate(
		[]core.FuzzyMatch{country},
		[]core.FuzzyMatch{town},
		noCountry,
		noTown,
	)
	if !hasPair(withException, "SG", "Singapore") {
		t.Fatalf("Generate did not include same-position pair with exception: %#v", withException)
	}
}

func TestCombinationCrossingOverlapPairIsGenerated(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	country := createFuzzyMatch("France", "France", "FR", 3, 8, 0.90, nil)
	town := createFuzzyMatch("Paris", "Paris", "FR", 0, 5, 0.88, nil)

	got := generator.Generate([]core.FuzzyMatch{country}, []core.FuzzyMatch{town}, noCountry, noTown)

	if !hasPair(got, "FR", "Paris") {
		t.Fatalf("Generate skipped crossing overlap pair: %#v", got)
	}
}

func TestCombinationGeneratorCopiesCountryTownSameName(t *testing.T) {
	sameName := map[string]string{"Singapore": "SG"}
	generator := newDefaultCombinationGenerator(sameName)
	sameName["Singapore"] = "MY"
	sameName["Monaco"] = "MC"

	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	country := createFuzzyMatch("Singapore", "Singapore", "SG", 0, 9, 0.95, nil)
	town := createFuzzyMatch("Singapore", "Singapore", "SG", 0, 9, 0.93, nil)

	got := generator.Generate([]core.FuzzyMatch{country}, []core.FuzzyMatch{town}, noCountry, noTown)

	if !hasPair(got, "SG", "Singapore") {
		t.Fatalf("Generate changed after caller mutated same-name map: %#v", got)
	}
}

func TestCombinationDedupesByCountryAndHyphenNormalizedTownName(t *testing.T) {
	generator := newDefaultCombinationGenerator(nil)
	noCountry := createFuzzyMatch("NO COUNTRY", "NO COUNTRY", "NO COUNTRY", 0, 0, 0.15, nil)
	noTown := createFuzzyMatch("NO TOWN", "NO TOWN", "NO TOWN", 0, 0, 0.15, nil)
	country := createFuzzyMatch("Monaco", "Monaco", "MC", 0, 6, 0.90, nil)
	towns := []core.FuzzyMatch{
		createFuzzyMatch("Monte-Carlo", "Monte-Carlo", "MC", 8, 19, 0.88, nil),
		createFuzzyMatch("Monte Carlo", "Monte Carlo", "MC", 21, 32, 0.96, nil),
	}

	got := generator.Generate([]core.FuzzyMatch{country}, towns, noCountry, noTown)

	var pairCount int
	for _, combo := range got {
		if combo.Country.Origin == "MC" && (combo.Town.Possibility == "Monte-Carlo" || combo.Town.Possibility == "Monte Carlo") {
			pairCount++
			if combo.Town.Possibility != "Monte Carlo" {
				t.Fatalf("Generate kept lower-scored duplicate town %q in %#v", combo.Town.Possibility, got)
			}
		}
	}
	if pairCount != 1 {
		t.Fatalf("Generate kept %d normalized duplicate MC/Monte Carlo pairs, want 1; combinations: %#v", pairCount, got)
	}
}

func newDefaultCombinationGenerator(countryTownSameName map[string]string) CombinationGenerator {
	cfg := config.Default()
	return NewCombinationGenerator(countryTownSameName, cfg.PostProcessing, cfg.TownWeights, cfg.CountryWeights)
}

func createFuzzyMatch(matched, possibility, origin string, start, end int, finalScore float64, flags []core.Flag) core.FuzzyMatch {
	return core.FuzzyMatch{
		Start:       start,
		End:         end,
		Matched:     matched,
		Possibility: possibility,
		Origin:      origin,
		FinalScore:  finalScore,
		Flags:       flags,
	}
}

func assertCombination(t *testing.T, got Combination, countryOrigin string, townPossibility string, score float64) {
	t.Helper()

	if got.Country.Origin != countryOrigin || got.Town.Possibility != townPossibility || math.Abs(got.Score-score) > 1e-9 {
		t.Fatalf("combination = (%q, %q, %f), want (%q, %q, %f)",
			got.Country.Origin, got.Town.Possibility, got.Score,
			countryOrigin, townPossibility, score)
	}
}

func assertSortedByScore(t *testing.T, got []Combination) {
	t.Helper()

	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Fatalf("combination %d score %f is greater than previous score %f in %#v", i, got[i].Score, got[i-1].Score, got)
		}
	}
}

func hasPair(combinations []Combination, countryOrigin string, townPossibility string) bool {
	for _, combo := range combinations {
		if combo.Country.Origin == countryOrigin && combo.Town.Possibility == townPossibility {
			return true
		}
	}
	return false
}
