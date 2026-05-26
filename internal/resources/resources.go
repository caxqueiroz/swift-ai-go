package resources

import (
	"compress/zlib"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/tipmarket/swift-ai/internal/normalize"
)

type Database struct {
	CountryAlpha2        map[string]string
	CountryPossibilities map[string][]string
	TownPossibilities    map[string]map[string]struct{}
	TownPopulations      map[string]int
	LargestTownCountry   map[string]string
	CountryTownSameName  map[string]string
	CountryGroupings     map[string][]string
	CountrySpecs         map[string]CountrySpec
	Provinces            map[string][]string
	Postcodes            map[string]map[string][]PostcodeEntry
}

type CountrySpec struct {
	Domains         []string `json:"domain_extensions"`
	PhonePrefixes   []string `json:"phone_prefixes"`
	PostalCodeRegex string   `json:"postal_code_regex"`
	IBAN            bool     `json:"iban"`
}

type PostcodeEntry struct {
	Town   string
	Origin string
}

type townParquetRow struct {
	Name        string `parquet:"name"`
	CountryCode string `parquet:"country code"`
	Population  int    `parquet:"population"`
}

func LoadCompressedJSON[T any](path string) (T, error) {
	var value T

	file, err := os.Open(path)
	if err != nil {
		return value, fmt.Errorf("open compressed JSON %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader, err := zlib.NewReader(file)
	if err != nil {
		return value, fmt.Errorf("open zlib stream %q: %w", path, err)
	}
	defer func() {
		_ = reader.Close()
	}()

	if err := json.NewDecoder(reader).Decode(&value); err != nil {
		return value, fmt.Errorf("decode compressed JSON %q: %w", path, err)
	}

	return value, nil
}

func LoadJSON[T any](path string) (T, error) {
	var value T

	file, err := os.Open(path)
	if err != nil {
		return value, fmt.Errorf("open JSON %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := json.NewDecoder(file).Decode(&value); err != nil {
		return value, fmt.Errorf("decode JSON %q: %w", path, err)
	}

	return value, nil
}

func LoadCountryAliases(countryAliasesPath string, provinceAliasesPath string) (alpha2 map[string]string, possibilities map[string][]string, err error) {
	countryAliases, err := LoadCompressedJSON[map[string][]string](countryAliasesPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load country aliases: %w", err)
	}

	provinceAliases, err := LoadCompressedJSON[map[string][]string](provinceAliasesPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load province aliases: %w", err)
	}

	alpha2 = make(map[string]string, len(countryAliases))
	possibilities = make(map[string][]string)

	for countryCode, aliases := range countryAliases {
		code := normalizeCountryCode(countryCode)
		if len(aliases) > 0 {
			alpha2[code] = cleanResourceName(aliases[0])
		}
		addAliases(possibilities, code, aliases)
	}

	for countryCode, aliases := range provinceAliases {
		addAliases(possibilities, normalizeCountryCode(countryCode), aliases)
	}

	addOrigin(possibilities, "NO COUNTRY", "")
	sortPossibilityOrigins(possibilities)

	return alpha2, possibilities, nil
}

func LoadTownsFromParquet(path string, aliases map[string][]string) (possibilities map[string]map[string]struct{}, populations map[string]int, largest map[string]string, err error) {
	rows, err := parquet.ReadFile[townParquetRow](path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read towns parquet %q: %w", path, err)
	}

	possibilities = make(map[string]map[string]struct{})
	populations = make(map[string]int)
	largest = make(map[string]string)

	largestPopulation := make(map[string]int)
	for _, row := range rows {
		countryCode := normalizeCountryCode(row.CountryCode)
		if countryCode == "" {
			continue
		}

		canonical := cleanResourceName(row.Name)
		if canonical == "" {
			continue
		}

		allAliases := aliasesForTown(canonical, aliases[canonical])
		for _, alias := range allAliases {
			addTownAlias(possibilities, countryCode, alias)
			if row.Population > populations[alias] {
				populations[alias] = row.Population
			}
			if row.Population > largestPopulation[alias] {
				largestPopulation[alias] = row.Population
				largest[alias] = countryCode
			}
		}
	}

	return possibilities, populations, largest, nil
}

func aliasesForTown(canonical string, extras []string) []string {
	aliasSet := make(map[string]struct{})
	addGeneratedAliases(aliasSet, canonical)
	for _, extra := range extras {
		addGeneratedAliases(aliasSet, cleanResourceName(extra))
	}

	aliases := make([]string, 0, len(aliasSet))
	for alias := range aliasSet {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func addAliases(possibilities map[string][]string, countryCode string, aliases []string) {
	for _, alias := range aliases {
		alias = cleanResourceName(alias)
		for _, duplicate := range normalize.GenerateDuplicateAliases(alias) {
			addOrigin(possibilities, duplicate, countryCode)
		}
	}
}

func addGeneratedAliases(values map[string]struct{}, alias string) {
	if alias == "" {
		return
	}
	for _, duplicate := range normalize.GenerateDuplicateAliases(alias) {
		values[duplicate] = struct{}{}
	}
}

func addOrigin(possibilities map[string][]string, alias string, origin string) {
	if alias == "" {
		return
	}
	for _, existing := range possibilities[alias] {
		if existing == origin {
			return
		}
	}
	possibilities[alias] = append(possibilities[alias], origin)
}

func sortPossibilityOrigins(possibilities map[string][]string) {
	for alias := range possibilities {
		sort.Strings(possibilities[alias])
	}
}

func addTownAlias(possibilities map[string]map[string]struct{}, countryCode string, alias string) {
	if alias == "" {
		return
	}
	if possibilities[countryCode] == nil {
		possibilities[countryCode] = make(map[string]struct{})
	}
	possibilities[countryCode][alias] = struct{}{}
}

func cleanResourceName(name string) string {
	return strings.ToUpper(strings.TrimSpace(normalize.DecodeAndClean(name)))
}

func normalizeCountryCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
