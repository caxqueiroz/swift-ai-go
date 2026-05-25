package postprocess

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/normalize"
	"github.com/tipmarket/swift-ai/internal/resources"
)

type Runner struct {
	cfg                 config.Config
	db                  resources.Database
	townFlagManager     TownFlagManager
	countryFlagManager  CountryFlagManager
	relationshipManager RelationshipFlagManager
	inclusionFlagger    MatchInclusionFlagger
	scoreComputer       ScoreComputer
}

func NewRunner(cfg config.Config, db resources.Database) Runner {
	return Runner{
		cfg:                 cfg,
		db:                  db,
		townFlagManager:     NewTownFlagManager(cfg.PostProcessing, db),
		countryFlagManager:  NewCountryFlagManager(cfg.PostProcessing, db),
		relationshipManager: NewRelationshipFlagManager(),
		inclusionFlagger:    MatchInclusionFlagger{},
		scoreComputer:       NewScoreComputer(cfg.TownWeights, cfg.CountryWeights),
	}
}

func (r Runner) Run(crf []core.CRFResult, fuzzy []core.FuzzyResult, postcodes [][]core.PostcodeMatch, samples []core.AddressSample) ([]core.Result, error) {
	if len(crf) != len(fuzzy) || len(crf) != len(postcodes) || len(crf) != len(samples) {
		return nil, fmt.Errorf("postprocess input length mismatch: crf=%d fuzzy=%d postcodes=%d samples=%d",
			len(crf), len(fuzzy), len(postcodes), len(samples))
	}

	results := make([]core.Result, 0, len(crf))
	for i := range crf {
		result, err := r.runOne(crf[i], fuzzy[i], postcodes[i], samples[i])
		if err != nil {
			return nil, fmt.Errorf("postprocess sample %d: %w", i, err)
		}
		results = append(results, result)
	}

	return results, nil
}

func (r Runner) runOne(crf core.CRFResult, fuzzy core.FuzzyResult, postcodes []core.PostcodeMatch, sample core.AddressSample) (core.Result, error) {
	text := crf.Details.Content
	if text == "" {
		text = sample.Text
	}

	processed := cloneFuzzyResult(fuzzy)
	processed.CountryMatches = scoreMatchesWithCRF(processed.CountryMatches, crf, core.TagCountry)
	processed.CountryCodeMatches = scoreMatchesWithCRF(processed.CountryCodeMatches, crf, core.TagCountry)

	r.processSuggestedCountry(&processed, sample)
	processed.CountryCodeMatches = filterCountryCodeMatches(processed.CountryCodeMatches, crf.Details.CountryCode)
	processed.CountryMatches = mergeMatches(processed.CountryMatches, processed.CountryCodeMatches)

	processed.ExtendedTownMatches = flagExtendedTownMatches(processed.ExtendedTownMatches)
	processed.TownMatches = mergeMatches(processed.TownMatches, processed.ExtendedTownMatches)
	processed.TownMatches = scoreMatchesWithCRF(processed.TownMatches, crf, core.TagTown)

	ibans, err := findIBANs(r.cfg.PostProcessing.IBANPattern, text)
	if err != nil {
		return core.Result{}, err
	}

	flagCRF := crfWithByteSpans(crf)
	processed.TownMatches = r.townFlagManager.ApplyTownFlags(processed.TownMatches, text, flagCRF)
	processed.CountryMatches = r.countryFlagManager.ApplyCountryFlags(processed.CountryMatches, text, flagCRF, ibans)
	processed.CountryMatches = filterFuzzyResults(r.inclusionFlagger, processed.CountryMatches, len(text))
	processed.TownMatches = filterFuzzyResults(r.inclusionFlagger, processed.TownMatches, len(text))

	countryCodes, nonCountryCodes := splitCountryListInCodeAndNotCode(processed.CountryMatches)
	processed.CountryMatches = mergeMatches(countryCodes, nonCountryCodes)

	countryHead := countryHeadForRelationship(crf)
	processed.TownMatches, processed.CountryMatches = r.relationshipManager.AddRelationshipFlags(
		processed.TownMatches,
		processed.CountryMatches,
		text,
		countryHead,
	)
	processed.TownMatches, processed.CountryMatches = r.relationshipManager.CheckReasonableMistakes(
		processed.TownMatches,
		processed.CountryMatches,
		crf,
	)
	addPostcodeTownFlags(processed.TownMatches, postcodes)

	processed.CountryMatches = r.computeCountryScores(processed.CountryMatches)
	processed.TownMatches = r.computeTownScores(processed.TownMatches)

	countryMatches, townMatches := r.finalMatches(processed.CountryMatches, processed.TownMatches, sample)
	processed.CountryMatches = countryMatches
	processed.TownMatches = townMatches

	return core.Result{
		CRFResult:             crf,
		FuzzyResult:           processed,
		IBANs:                 ibans,
		SuggestedCountry:      sample.SuggestedCountry,
		HasSuggestedCountry:   sample.HasSuggestedCountry,
		ForceSuggestedCountry: sample.ForceSuggestedCountry,
	}, nil
}

func (r Runner) processSuggestedCountry(fuzzy *core.FuzzyResult, sample core.AddressSample) {
	if !sample.HasSuggestedCountry || sample.SuggestedCountry == "" {
		return
	}

	for i := range fuzzy.CountryCodeMatches {
		match := &fuzzy.CountryCodeMatches[i]
		if sample.SuggestedCountry != match.Possibility || sample.SuggestedCountry != match.Origin {
			continue
		}
		addFlag(match, core.FlagIsSuggestedCountry)
		match.CRFScore = max(match.CRFScore, r.cfg.PostProcessing.BaseScoreSuggestedCountry)
	}

	if sample.SuggestedCountry == "NO COUNTRY" {
		return
	}

	position := len(sample.Text) + 2
	fuzzy.CountryCodeMatches = append(fuzzy.CountryCodeMatches, core.FuzzyMatch{
		Start:       position,
		End:         position,
		Matched:     "",
		Possibility: sample.SuggestedCountry,
		Origin:      sample.SuggestedCountry,
		CRFScore:    r.cfg.PostProcessing.BaseScoreSuggestedCountry,
		Flags:       []core.Flag{core.FlagGeneratedBySuggestedCountry},
	})
}

func (r Runner) computeCountryScores(matches []core.FuzzyMatch) []core.FuzzyMatch {
	scored := cloneMatches(matches)
	for i := range scored {
		if containsFlag(scored[i].Flags, core.FlagGeneratedBySuggestedCountry) {
			scored[i].Flags = []core.Flag{core.FlagGeneratedBySuggestedCountry}
		}
		scored[i].Flags = dedupeAndSortFlags(scored[i].Flags)
		scored[i].FinalScore = r.scoreComputer.ComputeCountryScore(scored[i].CRFScore, scored[i].Dist, scored[i].Flags)
	}
	return scored
}

func (r Runner) computeTownScores(matches []core.FuzzyMatch) []core.FuzzyMatch {
	scored := cloneMatches(matches)
	for i := range scored {
		scored[i].Flags = dedupeAndSortFlags(scored[i].Flags)
		scored[i].FinalScore = r.scoreComputer.ComputeTownScore(scored[i].CRFScore, scored[i].Dist, scored[i].Flags)
	}
	return scored
}

func (r Runner) finalMatches(countries, towns []core.FuzzyMatch, sample core.AddressSample) ([]core.FuzzyMatch, []core.FuzzyMatch) {
	countryThreshold := r.cfg.PostProcessing.MinimalFinalScoreCountry
	townThreshold := r.cfg.PostProcessing.MinimalFinalScoreTown
	countries = matchesAboveThreshold(countries, countryThreshold)
	towns = matchesAboveThreshold(towns, townThreshold)

	noCountryScore := r.cfg.PostProcessing.MinimalFinalScoreCountry
	if sample.HasSuggestedCountry && sample.SuggestedCountry == "NO COUNTRY" {
		noCountryScore = r.cfg.PostProcessing.BaseScoreSuggestedCountry
	}
	noCountry := core.FuzzyMatch{
		Start:       0,
		End:         0,
		Matched:     "",
		Possibility: "NO COUNTRY",
		Origin:      "NO COUNTRY",
		FinalScore:  noCountryScore,
	}
	noTown := core.FuzzyMatch{
		Start:       0,
		End:         0,
		Matched:     "",
		Possibility: "NO TOWN",
		Origin:      "NO TOWN",
		FinalScore:  r.cfg.PostProcessing.MinimalFinalScoreTown,
	}

	generator := NewCombinationGenerator(
		r.db.CountryTownSameName,
		r.cfg.PostProcessing,
		r.cfg.TownWeights,
		r.cfg.CountryWeights,
	)
	generator.SetSuggestedCountry(sample.SuggestedCountry, sample.HasSuggestedCountry)
	generator.SetForceSuggestedCountry(sample.ForceSuggestedCountry)
	combinations := generator.Generate(countries, towns, noCountry, noTown)

	finalCountries := make([]core.FuzzyMatch, 0, len(combinations))
	finalTowns := make([]core.FuzzyMatch, 0, len(combinations))
	for _, combination := range combinations {
		finalCountries = append(finalCountries, combination.Country)
		finalTowns = append(finalTowns, combination.Town)
	}

	return finalCountries, finalTowns
}

func scoreMatchesWithCRF(matches []core.FuzzyMatch, crf core.CRFResult, tag core.Tag) []core.FuzzyMatch {
	scored := cloneMatches(matches)
	logProbas := crf.LogProbasPerTag[tag]
	emissions := crf.EmissionsPerTag[tag]
	for i := range scored {
		start, end := byteSpanToRuneSpan(crf.Details.Content, scored[i].Start, scored[i].End)
		scored[i].CRFScore = meanSpan(logProbas, start, end)
		scored[i].TransformerScore = meanSpan(emissions, start, end)
	}
	return scored
}

func byteSpanToRuneSpan(text string, start int, end int) (int, int) {
	if text == "" {
		return start, end
	}
	start = clampIndex(start, len(text))
	end = clampIndex(end, len(text))
	if end < start {
		end = start
	}

	var runeStart int
	var runeEnd int
	for byteIndex := range text {
		if byteIndex < start {
			runeStart++
		}
		if byteIndex < end {
			runeEnd++
		}
	}

	return runeStart, runeEnd
}

func crfWithByteSpans(crf core.CRFResult) core.CRFResult {
	if crf.Details.Content == "" || len(crf.Details.Spans) == 0 {
		return crf
	}

	converted := crf
	converted.Details.Spans = make([]core.TaggedSpan, len(crf.Details.Spans))
	copy(converted.Details.Spans, crf.Details.Spans)
	for i := range converted.Details.Spans {
		start, end := runeSpanToByteSpan(crf.Details.Content, converted.Details.Spans[i].Start, converted.Details.Spans[i].End)
		converted.Details.Spans[i].Start = start
		converted.Details.Spans[i].End = end
	}

	return converted
}

func runeSpanToByteSpan(text string, start int, end int) (int, int) {
	if text == "" {
		return start, end
	}

	offsets := runeByteOffsets(text)
	start = clampIndex(start, len(offsets)-1)
	end = clampIndex(end, len(offsets)-1)
	if end < start {
		end = start
	}

	return offsets[start], offsets[end]
}

func runeByteOffsets(text string) []int {
	offsets := make([]int, 0, len(text)+1)
	for byteIndex := range text {
		offsets = append(offsets, byteIndex)
	}
	offsets = append(offsets, len(text))
	return offsets
}

func meanSpan(values []float64, start int, end int) float64 {
	if len(values) == 0 {
		return 0
	}
	start = clampIndex(start, len(values))
	end = clampIndex(end, len(values))
	if end <= start {
		return 0
	}

	var sum float64
	for _, value := range values[start:end] {
		sum += value
	}
	return sum / float64(end-start)
}

func filterCountryCodeMatches(matches []core.FuzzyMatch, countryHead string) []core.FuzzyMatch {
	filtered := make([]core.FuzzyMatch, 0, len(matches))
	for _, match := range matches {
		if match.CRFScore > 0 || match.Origin == countryHead {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func flagExtendedTownMatches(matches []core.FuzzyMatch) []core.FuzzyMatch {
	flagged := cloneMatches(matches)
	for i := range flagged {
		addFlag(&flagged[i], core.FlagIsFromExtendedData)
	}
	return flagged
}

func filterFuzzyResults(flagger MatchInclusionFlagger, matches []core.FuzzyMatch, textLength int) []core.FuzzyMatch {
	filtered := cloneMatches(matches)
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Dist != filtered[j].Dist {
			return filtered[i].Dist < filtered[j].Dist
		}
		return filtered[i].CRFScore > filtered[j].CRFScore
	})

	filtered = flagger.FlagMatchesIncludedInAnother(filtered, filtered)
	oneThird := float64(textLength) / 3
	twoThird := float64(textLength) * 2 / 3
	for i := range filtered {
		if filtered[i].End-filtered[i].Start <= 2 {
			addFlag(&filtered[i], core.FlagIsShort)
		}
		if float64(filtered[i].Start) <= oneThird {
			addFlag(&filtered[i], core.FlagIsInFirstThird)
		}
		if float64(filtered[i].Start) >= twoThird {
			addFlag(&filtered[i], core.FlagIsInLastThird)
		}
	}

	return filtered
}

func splitCountryListInCodeAndNotCode(countries []core.FuzzyMatch) ([]core.FuzzyMatch, []core.FuzzyMatch) {
	countryCodes := make([]core.FuzzyMatch, 0)
	nonCountryCodes := make([]core.FuzzyMatch, 0)
	for rank, country := range countries {
		if len(country.Possibility) <= 2 {
			countryCodes = append(countryCodes, flagCountryCodeIncludedInAnotherByRank(country, rank, countries))
			continue
		}
		nonCountryCodes = append(nonCountryCodes, country)
	}
	return countryCodes, nonCountryCodes
}

func flagCountryCodeIncludedInAnotherByRank(countryCode core.FuzzyMatch, rank int, countries []core.FuzzyMatch) core.FuzzyMatch {
	flagged := countryCode
	flagged.Flags = append([]core.Flag(nil), countryCode.Flags...)

	for otherRank, other := range countries {
		leftLarger := other.Start < flagged.Start && other.End >= flagged.End
		rightLarger := other.End > flagged.End && other.Start <= flagged.Start
		if !leftLarger && !rightLarger {
			continue
		}

		if rank <= otherRank && other.Dist < 1 {
			addFlag(&flagged, core.FlagIsInsideAnotherLowerRankedMatch)
		} else if rank > otherRank {
			addFlag(&flagged, core.FlagIsInsideAnotherHigherRankedMatch)
		}
	}

	return flagged
}

func countryHeadForRelationship(crf core.CRFResult) string {
	if crf.Details.CountryCode != "" && crf.Details.CountryCodeConfidence >= 0.99 {
		return crf.Details.CountryCode
	}
	return ""
}

func addPostcodeTownFlags(towns []core.FuzzyMatch, postcodes []core.PostcodeMatch) {
	for _, postcode := range postcodes {
		for i := range towns {
			addPostcodeFlagIfMatched(&towns[i], postcode)
		}
	}
}

func findIBANs(pattern string, text string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		unwrapped := unwrapPositiveLookaheadCapture(pattern)
		if unwrapped == pattern {
			return nil, fmt.Errorf("compile IBAN pattern %q: %w", pattern, err)
		}
		re, err = regexp.Compile(unwrapped)
		if err != nil {
			return nil, fmt.Errorf("compile IBAN pattern %q: %w", unwrapped, err)
		}
	}

	matches := re.FindAllStringSubmatch(text, -1)
	ibans := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			ibans = append(ibans, match[1])
			continue
		}
		ibans = append(ibans, match[0])
	}
	return ibans, nil
}

func unwrapPositiveLookaheadCapture(pattern string) string {
	if !strings.HasPrefix(pattern, "(?=(") || !strings.HasSuffix(pattern, "))") {
		return pattern
	}
	return pattern[len("(?=(") : len(pattern)-len("))")]
}

func addPostcodeFlagIfMatched(match *core.FuzzyMatch, postcode core.PostcodeMatch) {
	if postcode.Origin != match.Origin {
		return
	}
	for _, alias := range normalize.GenerateDuplicateAliases(postcode.Possibility) {
		if alias == match.Possibility {
			addFlag(match, core.FlagPostcodeForTownFound)
			return
		}
	}
}

func matchesAboveThreshold(matches []core.FuzzyMatch, threshold float64) []core.FuzzyMatch {
	filtered := make([]core.FuzzyMatch, 0, len(matches))
	for _, match := range matches {
		if match.FinalScore >= threshold {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func mergeMatches(left, right []core.FuzzyMatch) []core.FuzzyMatch {
	merged := make([]core.FuzzyMatch, 0, len(left)+len(right))
	merged = append(merged, cloneMatches(left)...)
	merged = append(merged, cloneMatches(right)...)
	return merged
}

func cloneFuzzyResult(result core.FuzzyResult) core.FuzzyResult {
	return core.FuzzyResult{
		CountryMatches:      cloneMatches(result.CountryMatches),
		CountryCodeMatches:  cloneMatches(result.CountryCodeMatches),
		TownMatches:         cloneMatches(result.TownMatches),
		ExtendedTownMatches: cloneMatches(result.ExtendedTownMatches),
	}
}

func dedupeAndSortFlags(flags []core.Flag) []core.Flag {
	seen := make(map[core.Flag]struct{}, len(flags))
	deduped := make([]core.Flag, 0, len(flags))
	for _, flag := range flags {
		if _, ok := seen[flag]; ok {
			continue
		}
		seen[flag] = struct{}{}
		deduped = append(deduped, flag)
	}
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i] < deduped[j]
	})
	return deduped
}
