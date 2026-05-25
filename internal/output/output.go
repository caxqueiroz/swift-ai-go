package output

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

const nBestMatches = 2

// WriteHumanReadable writes address structuring results as CSV or TSV based on path extension.
func WriteHumanReadable(path string, results []core.Result, showInferredCountry bool, verbose bool) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}

	writer := csv.NewWriter(file)
	if strings.EqualFold(filepath.Ext(path), ".tsv") {
		writer.Comma = '\t'
	}

	columns := humanReadableColumns(results, showInferredCountry, verbose)
	if err := writer.Write(columns); err != nil {
		return closeFile(file, fmt.Errorf("writing header: %w", err))
	}

	for _, result := range results {
		row := humanReadableRow(result, showInferredCountry, verbose)
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = row[column]
		}
		if err := writer.Write(values); err != nil {
			return closeFile(file, fmt.Errorf("writing row: %w", err))
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return closeFile(file, fmt.Errorf("flushing output: %w", err))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing output file: %w", err)
	}

	return nil
}

// WriteJSON writes address structuring results as pretty JSON.
func WriteJSON(path string, results []core.Result, verbose bool) error {
	payload := make([]jsonResult, len(results))
	for i, result := range results {
		payload[i] = newJSONResult(result, verbose)
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling json: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing json output: %w", err)
	}

	return nil
}

func humanReadableColumns(results []core.Result, showInferredCountry bool, verbose bool) []string {
	columns := []string{"address", "suggested_country", "force_suggested_country"}

	for i := 1; i <= nBestMatches; i++ {
		prefix := ordinalPrefix(i)
		columns = append(columns,
			prefix+"_best_country",
			prefix+"_best_country_confidence",
			prefix+"_best_country_resolved_code",
		)
		if showInferredCountry {
			columns = append(columns, prefix+"_inferred_country_resolved_code")
		}
		columns = append(columns,
			prefix+"_best_town",
			prefix+"_best_town_confidence",
			prefix+"_best_town_resolved",
		)
	}

	if verbose {
		columns = append(columns,
			"detailed_country_matches",
			"detailed_town_matches",
			"country_head_prediction",
			"country_head_confidence",
			"crf_spans",
			"ibans",
		)
		for _, tag := range verbosePredictionTags(results) {
			columns = append(columns, "crf_prediction_"+strings.ToLower(tag.String()))
		}
	}

	return columns
}

func humanReadableRow(result core.Result, showInferredCountry bool, verbose bool) map[string]string {
	row := map[string]string{
		"address": result.CRFResult.Details.Content,
	}
	if result.HasSuggestedCountry {
		row["suggested_country"] = result.SuggestedCountry
		row["force_suggested_country"] = fmt.Sprintf("%t", result.ForceSuggestedCountry)
	}

	for i := 0; i < nBestMatches; i++ {
		prefix := ordinalPrefix(i + 1)
		if i < len(result.FuzzyResult.CountryMatches) {
			match := result.FuzzyResult.CountryMatches[i]
			row[prefix+"_best_country"] = cleanMatched(match.Matched)
			row[prefix+"_best_country_confidence"] = formatConfidence(match.FinalScore)
			row[prefix+"_best_country_resolved_code"] = match.Origin
		}
		if showInferredCountry && i < len(result.FuzzyResult.TownMatches) {
			row[prefix+"_inferred_country_resolved_code"] = result.FuzzyResult.TownMatches[i].Origin
		}
		if i < len(result.FuzzyResult.TownMatches) {
			match := result.FuzzyResult.TownMatches[i]
			row[prefix+"_best_town"] = cleanMatched(match.Matched)
			row[prefix+"_best_town_confidence"] = formatConfidence(match.FinalScore)
			row[prefix+"_best_town_resolved"] = match.Possibility
		}
	}

	if verbose {
		row["detailed_country_matches"] = jsonText(result.FuzzyResult.CountryMatches)
		row["detailed_town_matches"] = jsonText(result.FuzzyResult.TownMatches)
		if result.CRFResult.Details.CountryCode != "" {
			row["country_head_prediction"] = result.CRFResult.Details.CountryCode
			row["country_head_confidence"] = formatConfidence(result.CRFResult.Details.CountryCodeConfidence)
		} else {
			row["country_head_prediction"] = "Country head disabled"
			row["country_head_confidence"] = "Country head disabled"
		}
		row["crf_spans"] = jsonText(result.CRFResult.Details)
		row["ibans"] = strings.Join(result.IBANs, "\n")
		for tag, predictions := range result.CRFResult.PredictionsPerTag {
			row["crf_prediction_"+strings.ToLower(tag.String())] = jsonText(predictions)
		}
	}

	return row
}

func cleanMatched(s string) string {
	return strings.ReplaceAll(s, "\n", "")
}

func formatConfidence(score float64) string {
	return fmt.Sprintf("%.2f%%", score*100)
}

func ordinalPrefix(i int) string {
	return fmt.Sprintf("%dth", i)
}

func verbosePredictionTags(results []core.Result) []core.Tag {
	tagsSeen := map[core.Tag]bool{
		core.TagCountry:    true,
		core.TagTown:       true,
		core.TagPostalCode: true,
	}
	for _, result := range results {
		for tag := range result.CRFResult.PredictionsPerTag {
			tagsSeen[tag] = true
		}
	}

	tags := make([]core.Tag, 0, len(tagsSeen))
	for tag := range tagsSeen {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].String() < tags[j].String()
	})
	return tags
}

func closeFile(file *os.File, err error) error {
	if closeErr := file.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("closing output file: %w", closeErr))
	}
	return err
}

func jsonText(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

type jsonResult struct {
	CRFResult             jsonCRFResult   `json:"crf_result"`
	FuzzyResult           jsonFuzzyResult `json:"fuzzy_result"`
	IBANs                 []string        `json:"ibans"`
	SuggestedCountry      string          `json:"suggested_country"`
	HasSuggestedCountry   bool            `json:"has_suggested_country"`
	ForceSuggestedCountry bool            `json:"force_suggested_country"`
}

type jsonCRFResult struct {
	Details           core.Details                      `json:"details"`
	PredictionsPerTag map[core.Tag][]core.PredictionCRF `json:"predictions_per_tag"`
	EmissionsPerTag   *map[core.Tag][]float64           `json:"emissions_per_tag,omitempty"`
	LogProbasPerTag   *map[core.Tag][]float64           `json:"log_probas_per_tag,omitempty"`
}

type jsonFuzzyResult struct {
	CountryMatches      []core.FuzzyMatch `json:"country_matches"`
	CountryCodeMatches  []core.FuzzyMatch `json:"country_code_matches"`
	TownMatches         []core.FuzzyMatch `json:"town_matches"`
	ExtendedTownMatches []core.FuzzyMatch `json:"extended_town_matches"`
}

func newJSONResult(result core.Result, verbose bool) jsonResult {
	crfResult := jsonCRFResult{
		Details:           result.CRFResult.Details,
		PredictionsPerTag: result.CRFResult.PredictionsPerTag,
	}
	if verbose {
		crfResult.EmissionsPerTag = &result.CRFResult.EmissionsPerTag
		crfResult.LogProbasPerTag = &result.CRFResult.LogProbasPerTag
	}

	return jsonResult{
		CRFResult: crfResult,
		FuzzyResult: jsonFuzzyResult{
			CountryMatches:      result.FuzzyResult.CountryMatches,
			CountryCodeMatches:  result.FuzzyResult.CountryCodeMatches,
			TownMatches:         result.FuzzyResult.TownMatches,
			ExtendedTownMatches: result.FuzzyResult.ExtendedTownMatches,
		},
		IBANs:                 result.IBANs,
		SuggestedCountry:      result.SuggestedCountry,
		HasSuggestedCountry:   result.HasSuggestedCountry,
		ForceSuggestedCountry: result.ForceSuggestedCountry,
	}
}
