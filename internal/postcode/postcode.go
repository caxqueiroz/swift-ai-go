package postcode

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

const (
	ArgentinaStructure = `[0-9]{4}`
	BrazilStructure    = `[0-9]{3}`
	ChileStructure     = `[0-9]{4}`
	ChinaStructure     = `[0-9]{2}`
	IrelandStructure   = `(?:[a-zA-Z][0-9][a-zA-Z][0-9]|[a-zA-Z]{2}[0-9]{2}|[a-zA-Z][0-9][a-zA-Z]{2})`
	MaltaStructure     = `[0-9]{4}`
)

type Entry struct {
	Town   string
	Origin string
}

type compiledRule struct {
	base *regexp.Regexp
	full *regexp.Regexp
}

func FindTownMatches(dict map[string][]Entry, regexes []string, text string, suffix string) ([]core.PostcodeMatch, error) {
	rules, err := compileRules(regexes, suffix)
	if err != nil {
		return nil, err
	}

	processed, processedToOriginal := preprocess(text)
	matches := make([]core.PostcodeMatch, 0)

	for _, rule := range rules {
		for _, loc := range rule.full.FindAllStringIndex(processed, -1) {
			matched := processed[loc[0]:loc[1]]
			key := rule.base.FindString(matched)
			if key == "" {
				continue
			}

			start := processedToOriginal[loc[0]]
			end := processedToOriginal[loc[1]]
			for _, entry := range dict[key] {
				matches = append(matches, core.PostcodeMatch{
					Start:       start,
					End:         end,
					Matched:     matched,
					Possibility: entry.Town,
					Origin:      entry.Origin,
				})
			}
		}
	}

	return matches, nil
}

func compileRules(regexes []string, suffix string) ([]compiledRule, error) {
	rules := make([]compiledRule, 0, len(regexes))
	for _, regexPattern := range regexes {
		baseRegex, err := regexp.Compile(regexPattern)
		if err != nil {
			return nil, fmt.Errorf("compile postcode base regex %q: %w", regexPattern, err)
		}

		fullPattern := regexPattern + suffix
		fullRegex, err := regexp.Compile(fullPattern)
		if err != nil {
			return nil, fmt.Errorf("compile postcode regex %q: %w", fullPattern, err)
		}

		rules = append(rules, compiledRule{
			base: baseRegex,
			full: fullRegex,
		})
	}

	return rules, nil
}

func preprocess(text string) (string, []int) {
	var b strings.Builder
	b.Grow(len(text))
	processedToOriginal := make([]int, 0, len(text)+1)

	for originalOffset, r := range text {
		processedToOriginal = append(processedToOriginal, originalOffset)
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	processedToOriginal = append(processedToOriginal, len(text))

	return b.String(), processedToOriginal
}
