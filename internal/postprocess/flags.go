package postprocess

import (
	"regexp"
	"strings"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/normalize"
	"github.com/tipmarket/swift-ai/internal/resources"
)

const relationshipCloseDistance = 15

type TownFlagManager struct {
	cfg config.PostProcessingConfig
	db  resources.Database
}

func NewTownFlagManager(cfg config.PostProcessingConfig, db resources.Database) TownFlagManager {
	return TownFlagManager{
		cfg: cfg,
		db:  db,
	}
}

func (m TownFlagManager) ApplyTownFlags(matches []core.FuzzyMatch, address string, crf core.CRFResult) []core.FuzzyMatch {
	flagged := cloneMatches(matches)
	for i := range flagged {
		addSeparatorTypoFlag(&flagged[i])
		m.addPopulationFlags(&flagged[i])
		m.addStreetIntersectionFlag(&flagged[i], crf)
		addTownAloneOnLineFlag(&flagged[i], address)
	}

	return flagged
}

func (m TownFlagManager) addPopulationFlags(match *core.FuzzyMatch) {
	townName := cleanLookupName(match.Possibility)
	population, ok := m.db.TownPopulations[townName]
	if !ok {
		return
	}

	if population >= m.cfg.IsMetropolisThreshold {
		addFlag(match, core.FlagIsMetropolis)
	}
	if population <= m.cfg.IsSmallTownThreshold {
		addFlag(match, core.FlagIsSmallTown)
	}
	if match.Origin != m.db.LargestTownCountry[townName] {
		addFlag(match, core.FlagIsNotLargestTownWithName)
	}
}

func addTownAloneOnLineFlag(match *core.FuzzyMatch, address string) {
	start := clampIndex(match.Start, len(address))
	end := clampIndex(match.End, len(address))
	if end < start {
		end = start
	}

	before := address[:start]
	after := address[end:]

	lineStart := strings.LastIndex(before, "\n")
	beforeLine := before
	if lineStart >= 0 {
		beforeLine = before[lineStart+1:]
	}

	lineEnd := strings.Index(after, "\n")
	afterLine := after
	if lineEnd >= 0 {
		afterLine = after[:lineEnd]
	}

	if strings.TrimSpace(beforeLine) == "" && strings.TrimSpace(afterLine) == "" {
		addFlag(match, core.FlagIsAloneOnLine)
	}
}

type CountryFlagManager struct {
	cfg config.PostProcessingConfig
	db  resources.Database
}

func NewCountryFlagManager(cfg config.PostProcessingConfig, db resources.Database) CountryFlagManager {
	return CountryFlagManager{
		cfg: cfg,
		db:  db,
	}
}

func (m CountryFlagManager) ApplyCountryFlags(matches []core.FuzzyMatch, address string, crf core.CRFResult, ibans []string) []core.FuzzyMatch {
	flagged := cloneMatches(matches)
	addressFold := strings.ToLower(address)

	for i := range flagged {
		addSeparatorTypoFlag(&flagged[i])
		m.addIBANFlag(&flagged[i], ibans)
		m.addStreetIntersectionFlag(&flagged[i], crf)
		m.addProvinceAliasFlags(&flagged[i])
		m.addCRFAgreementFlags(&flagged[i], crf)
		m.addFeatureFlags(&flagged[i], address, addressFold, crf)
	}

	return flagged
}

func (m CountryFlagManager) addIBANFlag(match *core.FuzzyMatch, ibans []string) {
	if match.Origin == "" {
		return
	}

	for _, iban := range ibans {
		if len(iban) >= 2 && strings.EqualFold(iban[:2], match.Origin) {
			addFlag(match, core.FlagIBANIsPresent)
			return
		}
	}
}

func (m CountryFlagManager) addProvinceAliasFlags(match *core.FuzzyMatch) {
	if len(match.Possibility) > 2 || match.Origin == "" {
		return
	}

	aliases, ok := m.db.Provinces[match.Origin]
	if !ok {
		return
	}

	possibility := cleanLookupName(match.Possibility)
	for _, alias := range aliases {
		if cleanLookupName(alias) != possibility {
			continue
		}
		if stringSliceContains(m.cfg.CountriesWithCommonProvinces, match.Origin) {
			addFlag(match, core.FlagIsCommonStateProvinceAlias)
			return
		}
		addFlag(match, core.FlagIsUncommonStateProvinceAlias)
		return
	}
}

func (m CountryFlagManager) addCRFAgreementFlags(match *core.FuzzyMatch, crf core.CRFResult) {
	if match.Origin == "" || match.Origin != crf.Details.CountryCode {
		return
	}

	countryHeadScore := crf.Details.CountryCodeConfidence * 100
	switch {
	case countryHeadScore >= 99:
		addFlag(match, core.FlagMLPStronglyAgrees)
	case countryHeadScore >= 90:
		addFlag(match, core.FlagMLPAgrees)
	case countryHeadScore >= 50:
		addFlag(match, core.FlagMLPDoesntDisagree)
	}
}

func (m CountryFlagManager) addFeatureFlags(match *core.FuzzyMatch, address string, addressFold string, crf core.CRFResult) {
	if match.Origin == "" {
		return
	}

	spec, ok := m.db.CountrySpecs[match.Origin]
	if !ok {
		return
	}

	for _, prefix := range spec.PhonePrefixes {
		if prefix != "" && strings.Contains(address, prefix) {
			addFlag(match, core.FlagPhonePrefixIsPresent)
			break
		}
	}

	for _, domain := range spec.Domains {
		if domain != "" && strings.Contains(addressFold, strings.ToLower(domain)) {
			addFlag(match, core.FlagDomainIsPresent)
			break
		}
	}

	if spec.PostalCodeRegex == "" {
		return
	}

	pattern, err := regexp.Compile(spec.PostalCodeRegex)
	if err != nil {
		return
	}
	for _, prediction := range crf.PredictionsPerTag[core.TagPostalCode] {
		if pattern.MatchString(prediction.Prediction) {
			addFlag(match, core.FlagPostalCodeIsPresent)
			return
		}
	}
}

type RelationshipFlagManager struct {
	closeDistance int
}

func NewRelationshipFlagManager() RelationshipFlagManager {
	return RelationshipFlagManager{
		closeDistance: relationshipCloseDistance,
	}
}

func (m RelationshipFlagManager) AddRelationshipFlags(towns, countries []core.FuzzyMatch, address string, countryHead string) ([]core.FuzzyMatch, []core.FuzzyMatch) {
	flaggedTowns := cloneMatches(towns)
	flaggedCountries := cloneMatches(countries)

	for i := range flaggedTowns {
		for j := range flaggedCountries {
			m.addPairFlags(&flaggedTowns[i], &flaggedCountries[j], address)
		}
		if countryHead != "" && countryHead == flaggedTowns[i].Origin {
			addFlag(&flaggedTowns[i], core.FlagMLPCountryIsPresent)
		}
	}

	return flaggedTowns, flaggedCountries
}

func (m RelationshipFlagManager) CheckReasonableMistakes(towns, countries []core.FuzzyMatch, crf core.CRFResult) ([]core.FuzzyMatch, []core.FuzzyMatch) {
	flaggedTowns := cloneMatches(towns)
	flaggedCountries := cloneMatches(countries)

	for i := range flaggedTowns {
		for _, prediction := range crf.PredictionsPerTag[core.TagCountry] {
			if strings.Contains(prediction.Prediction, flaggedTowns[i].Matched) {
				addFlag(&flaggedTowns[i], core.FlagCouldBeReasonableMistake)
				break
			}
		}
	}

	for i := range flaggedCountries {
		for _, prediction := range crf.PredictionsPerTag[core.TagTown] {
			if strings.Contains(prediction.Prediction, flaggedCountries[i].Matched) {
				addFlag(&flaggedCountries[i], core.FlagCouldBeReasonableMistake)
				break
			}
		}
	}

	return flaggedTowns, flaggedCountries
}

func (m RelationshipFlagManager) addPairFlags(town, country *core.FuzzyMatch, address string) {
	if !m.validRelationshipPair(*town, *country) {
		return
	}

	isExtendedData := containsFlag(town.Flags, core.FlagIsFromExtendedData)
	if containsFlag(country.Flags, core.FlagIsSuggestedCountry) || containsFlag(country.Flags, core.FlagGeneratedBySuggestedCountry) {
		addFlag(town, core.FlagSuggestedCountryIsPresent)
	}
	addFlag(town, core.FlagCountryIsPresent)
	if !isExtendedData {
		addFlag(country, core.FlagTownIsPresent)
	}

	if containsFlag(country.Flags, core.FlagIsCommonStateProvinceAlias) ||
		containsFlag(country.Flags, core.FlagIsUncommonStateProvinceAlias) {
		return
	}

	between := stringBetweenMatches(address, *town, *country)
	if len(between) <= m.closeDistance && !containsFlag(country.Flags, core.FlagGeneratedBySuggestedCountry) {
		addFlag(town, core.FlagIsVeryCloseToCountry)
		if !isExtendedData {
			addFlag(country, core.FlagIsVeryCloseToTown)
		}
	}
	if !strings.Contains(between, "\n") && !containsFlag(country.Flags, core.FlagGeneratedBySuggestedCountry) {
		addFlag(town, core.FlagIsOnSameLineAsCountry)
		if !isExtendedData {
			addFlag(country, core.FlagIsOnSameLineAsTown)
		}
	}
}

func (m RelationshipFlagManager) validRelationshipPair(town, country core.FuzzyMatch) bool {
	if country.Dist > 0 || town.Dist > 0 || containsFlag(town.Flags, core.FlagIsInsideAnotherWord) {
		return false
	}
	if containsFlag(country.Flags, core.FlagIsShort) && containsFlag(country.Flags, core.FlagIsInsideAnotherWord) {
		return false
	}
	if country.Origin == "" || country.Origin != town.Origin {
		return false
	}

	return true
}

type MatchInclusionFlagger struct{}

func (MatchInclusionFlagger) FlagMatchesIncludedInAnother(queries []core.FuzzyMatch, largerMatches []core.FuzzyMatch) []core.FuzzyMatch {
	flagged := cloneMatches(queries)

	for i := range flagged {
		for j, other := range largerMatches {
			leftLarger := other.Start < flagged[i].Start && other.End >= flagged[i].End
			rightLarger := other.End > flagged[i].End && other.Start <= flagged[i].Start
			if !leftLarger && !rightLarger {
				continue
			}

			if i <= j && other.Dist < 1 {
				addFlag(&flagged[i], core.FlagIsInsideAnotherLowerRankedMatch)
			} else if i > j {
				addFlag(&flagged[i], core.FlagIsInsideAnotherHigherRankedMatch)
			}
		}
	}

	return flagged
}

func (m TownFlagManager) addStreetIntersectionFlag(match *core.FuzzyMatch, crf core.CRFResult) {
	addStreetIntersectionFlag(match, crf, m.cfg.PartOfStreetRatio)
}

func (m CountryFlagManager) addStreetIntersectionFlag(match *core.FuzzyMatch, crf core.CRFResult) {
	addStreetIntersectionFlag(match, crf, m.cfg.PartOfStreetRatio)
}

func addStreetIntersectionFlag(match *core.FuzzyMatch, crf core.CRFResult, threshold float64) {
	matchLength := match.End - match.Start
	if matchLength <= 0 {
		return
	}

	var streetOverlap int
	for _, span := range crf.Details.Spans {
		if span.Start > match.End {
			break
		}
		if span.Tag != core.TagStreet {
			continue
		}

		overlapStart := max(span.Start, match.Start)
		overlapEnd := min(span.End, match.End)
		if overlapStart < overlapEnd {
			streetOverlap += overlapEnd - overlapStart
		}
	}

	if streetOverlap > 0 && float64(streetOverlap)/float64(matchLength) >= threshold {
		addFlag(match, core.FlagIsInsideStreet)
	}
}

func addSeparatorTypoFlag(match *core.FuzzyMatch) {
	if match.Dist <= 0 {
		return
	}
	if removeSeparators(match.Matched) == removeSeparators(match.Possibility) {
		addFlag(match, core.FlagIsSeparatorTypo)
	}
}

func removeSeparators(value string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(value))
}

func stringBetweenMatches(address string, left, right core.FuzzyMatch) string {
	if left.Start <= right.Start {
		start := clampIndex(left.End, len(address))
		end := clampIndex(right.Start, len(address))
		if end < start {
			return ""
		}
		return address[start:end]
	}

	start := clampIndex(right.End, len(address))
	end := clampIndex(left.Start, len(address))
	if end < start {
		return ""
	}
	return address[start:end]
}

func cleanLookupName(value string) string {
	return strings.ToUpper(strings.TrimSpace(normalize.DecodeAndClean(value)))
}

func cloneMatches(matches []core.FuzzyMatch) []core.FuzzyMatch {
	cloned := make([]core.FuzzyMatch, len(matches))
	copy(cloned, matches)
	for i := range cloned {
		cloned[i].Flags = append([]core.Flag(nil), cloned[i].Flags...)
	}
	return cloned
}

func addFlag(match *core.FuzzyMatch, flag core.Flag) {
	if containsFlag(match.Flags, flag) {
		return
	}
	match.Flags = append(match.Flags, flag)
}

func containsFlag(flags []core.Flag, want core.Flag) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func clampIndex(index int, length int) int {
	if index < 0 {
		return 0
	}
	if index > length {
		return length
	}
	return index
}
