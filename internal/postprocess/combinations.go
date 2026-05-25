package postprocess

import (
	"sort"
	"strings"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
)

type Combination struct {
	Country core.FuzzyMatch
	Town    core.FuzzyMatch
	Score   float64
}

type CombinationGenerator struct {
	CountryTownSameName   map[string]string
	Config                config.PostProcessingConfig
	TownWeights           config.TownWeights
	CountryWeights        config.CountryWeights
	SuggestedCountry      string
	HasSuggestedCountry   bool
	ForceSuggestedCountry bool
}

func NewCombinationGenerator(countryTownSameName map[string]string, cfg config.PostProcessingConfig, townWeights config.TownWeights, countryWeights config.CountryWeights) CombinationGenerator {
	countryTownSameNameCopy := make(map[string]string, len(countryTownSameName))
	for town, country := range countryTownSameName {
		countryTownSameNameCopy[town] = country
	}

	return CombinationGenerator{
		CountryTownSameName: countryTownSameNameCopy,
		Config:              cfg,
		TownWeights:         townWeights,
		CountryWeights:      countryWeights,
	}
}

func (g *CombinationGenerator) SetSuggestedCountry(country string, ok bool) {
	g.SuggestedCountry = country
	g.HasSuggestedCountry = ok
}

func (g *CombinationGenerator) SetForceSuggestedCountry(force bool) {
	g.ForceSuggestedCountry = force
}

func (g CombinationGenerator) Generate(countries, towns []core.FuzzyMatch, noCountry, noTown core.FuzzyMatch) []Combination {
	combinations := make([]Combination, 0, len(countries)*len(towns)+len(countries)+len(towns))

	for _, country := range countries {
		if !g.countryAllowed(country) {
			continue
		}
		for _, town := range towns {
			if country.Origin != town.Origin || g.shouldSkipPair(country, town) {
				continue
			}
			combinations = append(combinations, Combination{
				Country: country,
				Town:    town,
				Score:   (country.FinalScore + town.FinalScore) / 2,
			})
		}
	}

	for _, country := range countries {
		if !g.countryAllowed(country) {
			continue
		}
		combinations = append(combinations, Combination{
			Country: country,
			Town:    noTown,
			Score:   g.soloCountryScore(country),
		})
	}

	if !g.forcedNonNoCountry() {
		for _, town := range towns {
			combinations = append(combinations, Combination{
				Country: noCountry,
				Town:    town,
				Score:   g.soloTownScore(town),
			})
		}
	}

	if len(combinations) == 0 {
		combinations = append(combinations, Combination{
			Country: noCountry,
			Town:    noTown,
			Score:   (g.Config.MinimalFinalScoreCountry + g.Config.MinimalFinalScoreTown) / 2,
		})
	}

	sort.SliceStable(combinations, func(i, j int) bool {
		return combinations[i].Score > combinations[j].Score
	})

	return dedupeCombinations(combinations)
}

func (g CombinationGenerator) countryAllowed(country core.FuzzyMatch) bool {
	if !g.forcedSuggestedCountry() {
		return true
	}
	return country.Origin == g.SuggestedCountry
}

func (g CombinationGenerator) forcedSuggestedCountry() bool {
	return g.ForceSuggestedCountry && g.HasSuggestedCountry && g.SuggestedCountry != ""
}

func (g CombinationGenerator) forcedNonNoCountry() bool {
	return g.forcedSuggestedCountry() && g.SuggestedCountry != "NO COUNTRY"
}

func (g CombinationGenerator) shouldSkipPair(country, town core.FuzzyMatch) bool {
	if country.Start == town.Start && country.End == town.End {
		return g.CountryTownSameName[town.Possibility] != country.Origin
	}

	return containsSpan(country.Start, country.End, town.Start, town.End) ||
		containsSpan(town.Start, town.End, country.Start, country.End)
}

func containsSpan(outerStart, outerEnd, innerStart, innerEnd int) bool {
	return (innerStart > outerStart && innerEnd <= outerEnd) ||
		(innerStart >= outerStart && innerEnd < outerEnd)
}

func (g CombinationGenerator) soloCountryScore(country core.FuzzyMatch) float64 {
	flagSet := newFlagSet(country.Flags)
	cumulativeMalus := 0.0
	if flagSet[core.FlagTownIsPresent] {
		cumulativeMalus += g.CountryWeights.TownIsPresent
	}
	if flagSet[core.FlagIsVeryCloseToTown] {
		cumulativeMalus += g.CountryWeights.IsVeryCloseToTown
	}
	if flagSet[core.FlagIsOnSameLineAsTown] {
		cumulativeMalus += g.CountryWeights.IsOnSameLineAsTown
	}

	return (country.FinalScore + g.Config.MinimalFinalScoreTown - g.Config.NoTownFoundMul*cumulativeMalus) / 2
}

func (g CombinationGenerator) soloTownScore(town core.FuzzyMatch) float64 {
	flagSet := newFlagSet(town.Flags)
	cumulativeMalus := 0.0
	if flagSet[core.FlagCountryIsPresent] {
		cumulativeMalus += g.TownWeights.CountryIsPresentBonus
	}
	if flagSet[core.FlagSuggestedCountryIsPresent] {
		cumulativeMalus += g.TownWeights.SuggestedCountryIsPresentBonus
	}
	if flagSet[core.FlagIsVeryCloseToCountry] {
		cumulativeMalus += g.TownWeights.IsVeryCloseToCountry
	}
	if flagSet[core.FlagIsOnSameLineAsCountry] {
		cumulativeMalus += g.TownWeights.IsOnSameLineAsCountry
	}

	return (g.Config.MinimalFinalScoreCountry + town.FinalScore - g.Config.NoCountryFoundMul*cumulativeMalus) / 2
}

func dedupeCombinations(combinations []Combination) []Combination {
	seen := make(map[string]bool, len(combinations))
	deduped := make([]Combination, 0, len(combinations))

	for _, combination := range combinations {
		key := combination.Country.Origin + "\x00" + normalizeTownName(combination.Town.Possibility)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, combination)
	}

	return deduped
}

func normalizeTownName(town string) string {
	return strings.ReplaceAll(town, "-", " ")
}
