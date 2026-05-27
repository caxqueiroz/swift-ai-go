package readers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

const maxOpenAddressesLineBytes = 10 * 1024 * 1024

type OpenAddressesOptions struct {
	CountryCode string
	MaxRecords  int
}

type openAddressesFeature struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

func ReadOpenAddressesGeoJSON(path string, opts OpenAddressesOptions) ([]core.AddressSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open OpenAddresses GeoJSON file %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	return ReadOpenAddressesGeoJSONFromReader(file, opts)
}

func ReadOpenAddressesGeoJSONFromReader(r io.Reader, opts OpenAddressesOptions) ([]core.AddressSample, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxOpenAddressesLineBytes)

	var samples []core.AddressSample
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		sample, ok, err := ParseOpenAddressesFeature(line, opts.CountryCode)
		if err != nil {
			return nil, fmt.Errorf("parse OpenAddresses line %d: %w", lineNumber, err)
		}
		if !ok {
			continue
		}
		samples = append(samples, sample)
		if opts.MaxRecords > 0 && len(samples) >= opts.MaxRecords {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan OpenAddresses GeoJSON: %w", err)
	}
	return samples, nil
}

func ParseOpenAddressesFeature(line []byte, countryCode string) (core.AddressSample, bool, error) {
	var feature openAddressesFeature
	if err := json.Unmarshal(line, &feature); err != nil {
		return core.AddressSample{}, false, err
	}
	if len(feature.Properties) == 0 {
		return core.AddressSample{}, false, nil
	}

	number := propertyString(feature.Properties, "number")
	street := propertyString(feature.Properties, "street")
	unit := propertyString(feature.Properties, "unit")
	city := propertyString(feature.Properties, "city")
	district := propertyString(feature.Properties, "district")
	region := propertyString(feature.Properties, "region")
	postcode := propertyString(feature.Properties, "postcode")

	var lines []string
	appendUniqueLine(&lines, strings.TrimSpace(strings.Join(nonEmpty(number, street), " ")))
	if !isNilValue(unit) {
		appendUniqueLine(&lines, unit)
	}
	appendUniqueLine(&lines, strings.TrimSpace(strings.Join(nonEmpty(postcode, city), " ")))
	appendUniqueLine(&lines, district)
	appendUniqueLine(&lines, region)

	text := strings.Join(lines, "\n")
	if text == "" {
		return core.AddressSample{}, false, nil
	}

	sample := core.AddressSample{Text: text}
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if countryCode != "" {
		sample.SuggestedCountry = countryCode
		sample.HasSuggestedCountry = true
	}
	return sample, true, nil
}

func propertyString(properties map[string]json.RawMessage, name string) string {
	for key, raw := range properties {
		if !strings.EqualFold(key, name) {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if err := json.Unmarshal(raw, &number); err == nil {
			return strings.TrimSpace(number.String())
		}
		return ""
	}
	return ""
}

func nonEmpty(values ...string) []string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return parts
}

func appendUniqueLine(lines *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || isNilValue(value) {
		return
	}
	for _, existing := range *lines {
		if strings.EqualFold(existing, value) {
			return
		}
	}
	*lines = append(*lines, value)
}

func isNilValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "nil", "null", "none", "n/a", "na":
		return true
	default:
		return false
	}
}
