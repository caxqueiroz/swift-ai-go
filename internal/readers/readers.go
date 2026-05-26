package readers

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
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
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open text file %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReader(file)
	var samples []core.AddressSample
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("read text file %q: %w", path, err)
		}
		if len(line) > 0 {
			hadNewline := strings.HasSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\n")
			if hadNewline {
				line = strings.TrimSuffix(line, "\r")
			}
			samples = append(samples, core.AddressSample{Text: line})
		}
		if err == io.EOF {
			break
		}
	}
	return samples, nil
}

func ReadDelimited(path string, comma rune, addressColumn string) ([]core.AddressSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open delimited file %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader := csv.NewReader(file)
	reader.Comma = comma
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err == io.EOF {
		return nil, columnNotFoundError(path, addressColumn)
	}
	if err != nil {
		return nil, fmt.Errorf("read header from delimited file %q: %w", path, err)
	}

	addressIndex := columnIndex(header, addressColumn)
	if addressIndex < 0 {
		return nil, columnNotFoundError(path, addressColumn)
	}
	suggestedCountryIndex := columnIndex(header, DefaultSuggestedCountryColumn)
	forceSuggestedCountryIndex := columnIndex(header, DefaultForceSuggestedCountryColumn)

	var samples []core.AddressSample
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read record from delimited file %q: %w", path, err)
		}
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

func columnNotFoundError(path, column string) error {
	return fmt.Errorf("read delimited file %q: Column '%s' not found", path, column)
}

func columnIndex(header []string, column string) int {
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		if strings.TrimSpace(name) == column {
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
