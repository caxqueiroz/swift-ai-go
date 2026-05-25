package postprocess

import (
	"math"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
)

const (
	scoreEpsilon       = 1e-6
	crfMaxContribution = 0.9
	minBonusMul        = 2.5
	maxBonusMul        = 4.0
)

type ScoreComputer struct {
	TownWeights    config.TownWeights
	CountryWeights config.CountryWeights
}

func NewScoreComputer(town config.TownWeights, country config.CountryWeights) ScoreComputer {
	return ScoreComputer{
		TownWeights:    town,
		CountryWeights: country,
	}
}

func (s ScoreComputer) ComputeTownScore(crfScore float64, distScore int, flags []core.Flag) float64 {
	flagSet := newFlagSet(flags)
	bonuses, maluses := s.townContributions(distScore, flagSet)

	return computeScore(crfScore, bonuses, maluses)
}

func (s ScoreComputer) ComputeCountryScore(crfScore float64, distScore int, flags []core.Flag) float64 {
	flagSet := newFlagSet(flags)
	if flagSet[core.FlagGeneratedBySuggestedCountry] {
		return crfScore
	}

	bonuses, maluses := s.countryContributions(distScore, flagSet)

	return computeScore(crfScore, bonuses, maluses)
}

func (s ScoreComputer) townContributions(distScore int, flags map[core.Flag]bool) (float64, float64) {
	var bonuses float64
	var maluses float64

	add := func(weight float64) {
		if weight >= 0 {
			bonuses += weight
			return
		}
		maluses += weight
	}

	if distScore > 0 && !flags[core.FlagIsSeparatorTypo] {
		add(float64(distScore) * s.TownWeights.ContainsTypo)
	}
	if flags[core.FlagIsInLastThird] {
		add(s.TownWeights.IsInLastThird)
	}
	if flags[core.FlagCouldBeReasonableMistake] {
		add(s.TownWeights.CouldBeReasonableMistake)
	}
	if flags[core.FlagCountryIsPresent] {
		add(s.TownWeights.CountryIsPresentBonus)
	} else {
		maluses += s.TownWeights.CountryIsPresentMalus
	}
	if flags[core.FlagSuggestedCountryIsPresent] {
		add(s.TownWeights.SuggestedCountryIsPresentBonus)
	}
	if flags[core.FlagMLPCountryIsPresent] {
		add(s.TownWeights.MLPCountryIsPresentBonus)
	}
	if flags[core.FlagIsVeryCloseToCountry] {
		add(s.TownWeights.IsVeryCloseToCountry)
	}
	if flags[core.FlagIsOnSameLineAsCountry] {
		add(s.TownWeights.IsOnSameLineAsCountry)
	}
	if flags[core.FlagPostcodeForTownFound] {
		add(s.TownWeights.PostcodeForTownFound)
	}
	if flags[core.FlagIsMetropolis] {
		add(s.TownWeights.IsMetropolis)
	}
	if flags[core.FlagIsAloneOnLine] {
		add(s.TownWeights.IsAloneOnLine)
	}
	if flags[core.FlagIsInsideAnotherWord] {
		add(s.TownWeights.IsInsideAnotherWord)
	}
	if flags[core.FlagIsInFirstThird] {
		add(s.TownWeights.IsInFirstThird)
	}
	if flags[core.FlagIsShort] {
		add(s.TownWeights.IsShort)
	}
	if flags[core.FlagIsInsideAnotherLowerRankedMatch] {
		add(s.TownWeights.IsInsideAnotherLowerRankedMatch)
	}
	if flags[core.FlagIsSmallTown] {
		add(s.TownWeights.IsSmallTown)
		if !flags[core.FlagCountryIsPresent] {
			add(s.TownWeights.IsSmallTownAndCountryNotPresent)
		}
	}
	if flags[core.FlagIsFromExtendedData] {
		add(s.TownWeights.IsFromExtendedData)
	}
	if flags[core.FlagIsNotLargestTownWithName] {
		add(s.TownWeights.IsNotLargestTownWithName)
	}
	if flags[core.FlagIsInsideStreet] {
		add(s.TownWeights.IsInsideStreet)
	}
	if flags[core.FlagIsCommonStateProvinceAlias] {
		add(s.TownWeights.IsCommonStateProvinceAlias)
	}
	if flags[core.FlagIsUncommonStateProvinceAlias] {
		add(s.TownWeights.IsUncommonStateProvinceAlias)
	}
	if flags[core.FlagIsShort] && distScore > 0 {
		add(s.TownWeights.IsShortAndNonzeroDistScore)
	}
	if flags[core.FlagIsShort] && flags[core.FlagIsInsideAnotherWord] {
		add(s.TownWeights.IsShortAndIsInsideAnotherWord)
	}
	if flags[core.FlagIsInsideAnotherHigherRankedMatch] {
		add(s.TownWeights.IsInsideAnotherHigherRankedMatch)
	}

	return bonuses, maluses
}

func (s ScoreComputer) countryContributions(distScore int, flags map[core.Flag]bool) (float64, float64) {
	var bonuses float64
	var maluses float64

	add := func(weight float64) {
		if weight >= 0 {
			bonuses += weight
			return
		}
		maluses += weight
	}

	if distScore > 0 && !flags[core.FlagIsSeparatorTypo] {
		add(float64(distScore) * s.CountryWeights.ContainsTypo)
	}
	if flags[core.FlagIsInLastThird] {
		add(s.CountryWeights.IsInLastThird)
	}
	if flags[core.FlagCouldBeReasonableMistake] {
		add(s.CountryWeights.CouldBeReasonableMistake)
	}
	if flags[core.FlagTownIsPresent] {
		add(s.CountryWeights.TownIsPresent)
	}
	if flags[core.FlagIsVeryCloseToTown] {
		add(s.CountryWeights.IsVeryCloseToTown)
	}
	if flags[core.FlagIsOnSameLineAsTown] {
		add(s.CountryWeights.IsOnSameLineAsTown)
	}
	if flags[core.FlagPostalCodeIsPresent] {
		add(s.CountryWeights.PostalCodeIsPresent)
	}
	if flags[core.FlagIBANIsPresent] {
		add(s.CountryWeights.IBANIsPresent)
	}
	if flags[core.FlagPhonePrefixIsPresent] {
		add(s.CountryWeights.PhonePrefixIsPresent)
	}
	if flags[core.FlagDomainIsPresent] {
		add(s.CountryWeights.DomainIsPresent)
	}
	if flags[core.FlagMLPStronglyAgrees] {
		add(s.CountryWeights.MLPStronglyAgrees)
	}
	if flags[core.FlagMLPAgrees] {
		add(s.CountryWeights.MLPAgrees)
	}
	if flags[core.FlagMLPDoesntDisagree] {
		add(s.CountryWeights.MLPDoesntDisagree)
	}
	if flags[core.FlagIsInsideAnotherWord] {
		add(s.CountryWeights.IsInsideAnotherWord)
	}
	if flags[core.FlagIsInFirstThird] {
		add(s.CountryWeights.IsInFirstThird)
	}
	if flags[core.FlagIsShort] {
		add(s.CountryWeights.IsShort)
	}
	if flags[core.FlagIsInsideAnotherLowerRankedMatch] {
		add(s.CountryWeights.IsInsideAnotherLowerRankedMatch)
	}
	if flags[core.FlagIsInsideStreet] {
		add(s.CountryWeights.IsInsideStreet)
	}
	if flags[core.FlagIsCommonStateProvinceAlias] {
		add(s.CountryWeights.IsCommonStateProvinceAlias)
	}
	if flags[core.FlagIsUncommonStateProvinceAlias] {
		add(s.CountryWeights.IsUncommonStateProvinceAlias)
	}
	if flags[core.FlagIsShort] && distScore > 0 {
		add(s.CountryWeights.IsShortAndNonzeroDistScore)
	}
	if flags[core.FlagIsShort] && flags[core.FlagIsInsideAnotherWord] {
		add(s.CountryWeights.IsShortAndIsInsideAnotherWord)
	}
	if flags[core.FlagIsInsideAnotherHigherRankedMatch] {
		add(s.CountryWeights.IsInsideAnotherHigherRankedMatch)
	}

	return bonuses, maluses
}

func computeScore(crfScore float64, bonuses float64, maluses float64) float64 {
	crfScore = clip(crfScore, scoreEpsilon, 1-scoreEpsilon)
	amortizedCRF := clip(crfScore, 1-crfMaxContribution, crfMaxContribution)
	logOdds := math.Log(amortizedCRF / (1 - amortizedCRF))
	bonusMul := maxBonusMul - (maxBonusMul-minBonusMul)*crfScore
	malusMul := minBonusMul + (maxBonusMul-minBonusMul)*crfScore

	return 1 / (1 + math.Exp(-(logOdds + bonusMul*bonuses + malusMul*maluses)))
}

func clip(value float64, minValue float64, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func newFlagSet(flags []core.Flag) map[core.Flag]bool {
	flagSet := make(map[core.Flag]bool, len(flags))
	for _, flag := range flags {
		flagSet[flag] = true
	}
	return flagSet
}
