package normalize

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mozillazg/go-unidecode"
)

var saintPattern = regexp.MustCompile(`\b(?:SAINT|ST\.|ST)([^[:space:][:alnum:]_]|[[:space:]])`)

func DecodeAndClean(s string) string {
	replacer := strings.NewReplacer(
		"@", "a",
		"`", "'",
		"–", "-",
		"´", "'",
		"‘", "'",
		"’", "'",
	)

	return replacer.Replace(unidecode.Unidecode(s))
}

func DuplicateIfSeparatorPresent(name string) []string {
	aliases := []string{name}
	seen := map[string]struct{}{name: {}}

	if strings.Contains(name, "-") {
		addAlias(&aliases, seen, strings.ReplaceAll(name, "-", " "))
	}
	if strings.Contains(name, " ") {
		addAlias(&aliases, seen, strings.ReplaceAll(name, " ", "-"))
	}

	return aliases
}

func DuplicateIfSaintInName(name string) []string {
	aliases := []string{name}
	seen := map[string]struct{}{name: {}}

	if !saintPattern.MatchString(name) {
		return aliases
	}

	for _, replacement := range []string{"SAINT-", "ST. ", "ST-"} {
		addAlias(&aliases, seen, saintPattern.ReplaceAllString(name, replacement))
	}

	return aliases
}

func GenerateDuplicateAliases(name string) []string {
	values := []string{name}
	for _, saint := range DuplicateIfSaintInName(name) {
		values = append(values, DuplicateIfSeparatorPresent(saint)...)
	}

	return unique(values)
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	uniqueValues := make([]string, 0, len(values))

	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		uniqueValues = append(uniqueValues, value)
	}

	sort.Strings(uniqueValues)
	return uniqueValues
}

func addAlias(aliases *[]string, seen map[string]struct{}, alias string) {
	if _, ok := seen[alias]; ok {
		return
	}
	seen[alias] = struct{}{}
	*aliases = append(*aliases, alias)
}
