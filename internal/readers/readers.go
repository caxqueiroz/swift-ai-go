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
	DefaultIDColumn                    = "id"
	DefaultAddressColumn               = "address"
	DefaultSuggestedCountryColumn      = "suggested_country"
	DefaultForceSuggestedCountryColumn = "force_suggested_country"
)

type DelimitedOptions struct {
	AddressColumn               string
	IDColumn                    string
	SuggestedCountryColumn      string
	ForceSuggestedCountryColumn string
}

func DefaultDelimitedOptions() DelimitedOptions {
	return DelimitedOptions{
		AddressColumn:               DefaultAddressColumn,
		IDColumn:                    DefaultIDColumn,
		SuggestedCountryColumn:      DefaultSuggestedCountryColumn,
		ForceSuggestedCountryColumn: DefaultForceSuggestedCountryColumn,
	}
}

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
	opts := DefaultDelimitedOptions()
	opts.AddressColumn = addressColumn
	return ReadDelimitedWithOptions(path, comma, opts)
}

func ReadDelimitedWithOptions(path string, comma rune, opts DelimitedOptions) ([]core.AddressSample, error) {
	opts = normalizeDelimitedOptions(opts)
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
		return nil, columnNotFoundError(path, opts.AddressColumn)
	}
	if err != nil {
		return nil, fmt.Errorf("read header from delimited file %q: %w", path, err)
	}

	addressIndex := columnIndex(header, opts.AddressColumn)
	if addressIndex < 0 {
		return nil, columnNotFoundError(path, opts.AddressColumn)
	}
	idIndex := columnIndex(header, opts.IDColumn)
	suggestedCountryIndex := columnIndex(header, opts.SuggestedCountryColumn)
	forceSuggestedCountryIndex := columnIndex(header, opts.ForceSuggestedCountryColumn)

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
		if idIndex >= 0 && idIndex < len(record) {
			sample.ID = strings.TrimSpace(record[idIndex])
		}
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

func normalizeDelimitedOptions(opts DelimitedOptions) DelimitedOptions {
	defaults := DefaultDelimitedOptions()
	if opts.AddressColumn == "" {
		opts.AddressColumn = defaults.AddressColumn
	}
	if opts.IDColumn == "" {
		opts.IDColumn = defaults.IDColumn
	}
	if opts.SuggestedCountryColumn == "" {
		opts.SuggestedCountryColumn = defaults.SuggestedCountryColumn
	}
	if opts.ForceSuggestedCountryColumn == "" {
		opts.ForceSuggestedCountryColumn = defaults.ForceSuggestedCountryColumn
	}
	return opts
}

func columnNotFoundError(path, column string) error {
	return fmt.Errorf("read delimited file %q: Column '%s' not found", path, column)
}

func columnIndex(header []string, column string) int {
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
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
