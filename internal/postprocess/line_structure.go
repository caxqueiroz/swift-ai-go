package postprocess

import (
	"sort"
	"strings"
	"unicode"

	"github.com/tipmarket/swift-ai/internal/core"
)

const (
	finalCountryLineBonus      = 0.20
	penultimateTownLineBonus   = 0.30
	earlyTownLinePenalty       = 0.25
	nonFinalCountryLinePenalty = 0.20
	lineStructureTownScore     = 0.90
)

type lineSpan struct {
	start     int
	end       int
	trimStart int
	trimEnd   int
}

func rerankCombinationsForLineStructure(text string, combinations []Combination) []Combination {
	lines := nonEmptyLineSpans(text)
	if len(lines) < 2 || len(combinations) < 2 {
		return combinations
	}

	ranked := append([]Combination(nil), combinations...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i].Score + lineStructureAdjustment(lines, ranked[i])
		right := ranked[j].Score + lineStructureAdjustment(lines, ranked[j])
		return left > right
	})
	return ranked
}

func addPenultimateLineTownFallback(text string, countries, towns []core.FuzzyMatch) []core.FuzzyMatch {
	lines := nonEmptyLineSpans(text)
	if len(lines) < 2 || !looksLikeCountryCodeLine(lines[len(lines)-1]) {
		return towns
	}

	country, ok := finalLineCountryMatch(lines, countries)
	if !ok {
		return towns
	}

	townLine := lines[len(lines)-2]
	town := strings.TrimSpace(text[townLine.trimStart:townLine.trimEnd])
	if !containsLetter(town) || hasEquivalentTownOnLine(lines, towns, len(lines)-2, town, country.Origin) {
		return towns
	}

	withFallback := append([]core.FuzzyMatch(nil), towns...)
	withFallback = append(withFallback, core.FuzzyMatch{
		Start:            townLine.trimStart,
		End:              townLine.trimEnd,
		Matched:          town,
		Possibility:      town,
		Origin:           country.Origin,
		Flags:            []core.Flag{core.FlagIsAloneOnLine},
		CRFScore:         lineStructureTownScore,
		TransformerScore: lineStructureTownScore,
		FinalScore:       lineStructureTownScore,
	})
	return withFallback
}

func finalLineCountryMatch(lines []lineSpan, countries []core.FuzzyMatch) (core.FuzzyMatch, bool) {
	lastLine := len(lines) - 1
	for _, country := range countries {
		if country.Origin == "" || country.Origin == "NO COUNTRY" {
			continue
		}
		if lineIndexForSpan(lines, country.Start, country.End) != lastLine {
			continue
		}
		if matchOccupiesTrimmedLine(lines[lastLine], country.Start, country.End) {
			return country, true
		}
	}
	return core.FuzzyMatch{}, false
}

func hasEquivalentTownOnLine(lines []lineSpan, towns []core.FuzzyMatch, lineIndex int, town string, country string) bool {
	want := cleanLookupName(town)
	for _, candidate := range towns {
		if candidate.Origin != country {
			continue
		}
		if lineIndexForSpan(lines, candidate.Start, candidate.End) != lineIndex {
			continue
		}
		if cleanLookupName(candidate.Possibility) == want {
			return true
		}
	}
	return false
}

func lineStructureAdjustment(lines []lineSpan, combination Combination) float64 {
	lastLine := len(lines) - 1
	townLine := lastLine - 1
	countryLine := lineIndexForSpan(lines, combination.Country.Start, combination.Country.End)
	matchTownLine := lineIndexForSpan(lines, combination.Town.Start, combination.Town.End)

	var adjustment float64
	if countryLine == lastLine && matchOccupiesTrimmedLine(lines[countryLine], combination.Country.Start, combination.Country.End) {
		adjustment += finalCountryLineBonus
	} else if countryLine >= 0 && looksLikeCountryCodeLine(lines[lastLine]) {
		adjustment -= nonFinalCountryLinePenalty
	}

	if matchTownLine == townLine && matchOccupiesTrimmedLine(lines[matchTownLine], combination.Town.Start, combination.Town.End) {
		adjustment += penultimateTownLineBonus
	} else if countryLine == lastLine && matchTownLine >= 0 && matchTownLine < townLine {
		adjustment -= earlyTownLinePenalty
	}

	return adjustment
}

func nonEmptyLineSpans(text string) []lineSpan {
	lines := make([]lineSpan, 0, strings.Count(text, "\n")+1)
	start := 0
	for start <= len(text) {
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += start
		}

		trimStart, trimEnd := trimSpaceSpan(text, start, end)
		if trimStart < trimEnd {
			lines = append(lines, lineSpan{
				start:     start,
				end:       end,
				trimStart: trimStart,
				trimEnd:   trimEnd,
			})
		}

		if end == len(text) {
			break
		}
		start = end + 1
	}
	return lines
}

func trimSpaceSpan(text string, start int, end int) (int, int) {
	for start < end && isASCIISpace(text[start]) {
		start++
	}
	for end > start && isASCIISpace(text[end-1]) {
		end--
	}
	return start, end
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func lineIndexForSpan(lines []lineSpan, start int, end int) int {
	for i, line := range lines {
		if start >= line.start && end <= line.end {
			return i
		}
	}
	return -1
}

func matchOccupiesTrimmedLine(line lineSpan, start int, end int) bool {
	return start <= line.trimStart && end >= line.trimEnd
}

func looksLikeCountryCodeLine(line lineSpan) bool {
	return line.trimEnd-line.trimStart == 2
}

func containsLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
