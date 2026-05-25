package readers

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

const (
	DefaultAddressColumn               = "address"
	DefaultSuggestedCountryColumn      = "suggested_country"
	DefaultForceSuggestedCountryColumn = "force_suggested_country"
)

func ReadText(path string) ([]core.AddressSample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	samples := make([]core.AddressSample, 0, len(lines))
	for _, line := range lines {
		samples = append(samples, core.AddressSample{Text: line})
	}
	return samples, nil
}

func ReadDelimited(path string, comma rune, addressColumn string) ([]core.AddressSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = comma
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("Column '%s' not found", addressColumn)
	}

	header := records[0]
	addressIndex := columnIndex(header, addressColumn)
	if addressIndex < 0 {
		return nil, fmt.Errorf("Column '%s' not found", addressColumn)
	}
	suggestedCountryIndex := columnIndex(header, DefaultSuggestedCountryColumn)
	forceSuggestedCountryIndex := columnIndex(header, DefaultForceSuggestedCountryColumn)

	samples := make([]core.AddressSample, 0, len(records)-1)
	for _, record := range records[1:] {
		if addressIndex >= len(record) || record[addressIndex] == "" {
			continue
		}

		sample := core.AddressSample{Text: record[addressIndex]}
		if suggestedCountryIndex >= 0 && suggestedCountryIndex < len(record) {
			suggestedCountry := strings.ToUpper(strings.TrimSpace(record[suggestedCountryIndex]))
			if suggestedCountry != "" {
				sample.SuggestedCountry = suggestedCountry
				sample.HasSuggestedCountry = true
			}
		}
		if forceSuggestedCountryIndex >= 0 && forceSuggestedCountryIndex < len(record) {
			sample.ForceSuggestedCountry = parseForceFlag(record[forceSuggestedCountryIndex])
		}

		samples = append(samples, sample)
	}
	return samples, nil
}

func columnIndex(header []string, column string) int {
	for i, name := range header {
		if name == column {
			return i
		}
	}
	return -1
}

func parseForceFlag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true
	default:
		return false
	}
}
