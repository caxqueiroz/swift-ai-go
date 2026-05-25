package output

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

func TestWriteHumanReadableCSVEmitsCoreColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	results := []core.Result{sampleResult()}

	if err := WriteHumanReadable(path, results, false, false); err != nil {
		t.Fatalf("writing csv: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening csv: %v", err)
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("reading csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}

	header := rows[0]
	values := mapRow(header, rows[1])
	want := map[string]string{
		"address":                        "10 Downing Street\nLondon",
		"1th_best_country":               "United Kingdom",
		"1th_best_country_confidence":    "96.12%",
		"1th_best_country_resolved_code": "GB",
		"1th_best_town":                  "London",
		"1th_best_town_confidence":       "88.50%",
		"1th_best_town_resolved":         "London",
		"2th_best_country":               "France",
		"2th_best_country_confidence":    "61.00%",
		"2th_best_country_resolved_code": "FR",
		"suggested_country":              "GB",
		"force_suggested_country":        "true",
		"2th_best_town":                  "",
		"2th_best_town_confidence":       "",
		"2th_best_town_resolved":         "",
	}
	for column, expected := range want {
		assertHeaderContains(t, header, column)
		if got := values[column]; got != expected {
			t.Fatalf("%s = %q, want %q", column, got, expected)
		}
	}
	assertHeaderDoesNotContain(t, header, "1th_inferred_country_resolved_code")
	assertHeaderDoesNotContain(t, header, "2th_inferred_country_resolved_code")
}

func TestWriteHumanReadableTSVUsesTabSeparator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.tsv")

	if err := WriteHumanReadable(path, []core.Result{sampleResult()}, false, false); err != nil {
		t.Fatalf("writing tsv: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading tsv: %v", err)
	}
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.Contains(firstLine, "\t") {
		t.Fatalf("header %q does not contain tab separator", firstLine)
	}
	if strings.Contains(firstLine, ",") {
		t.Fatalf("header %q contains comma separator", firstLine)
	}
}

func TestWriteHumanReadableAlwaysEmitsSuggestedCountryColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	result := sampleResult()
	result.SuggestedCountry = ""
	result.HasSuggestedCountry = false
	result.ForceSuggestedCountry = false

	rows := readHumanReadable(t, path, []core.Result{result}, false, false)
	header := rows[0]
	values := mapRow(header, rows[1])

	assertHeaderContains(t, header, "suggested_country")
	assertHeaderContains(t, header, "force_suggested_country")
	if got := values["suggested_country"]; got != "" {
		t.Fatalf("suggested_country = %q, want empty", got)
	}
	if got := values["force_suggested_country"]; got != "" {
		t.Fatalf("force_suggested_country = %q, want empty", got)
	}
}

func TestWriteHumanReadableInferredCountryColumnsFollowOption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")

	rows := readHumanReadable(t, path, []core.Result{sampleResult()}, true, false)
	header := rows[0]
	values := mapRow(header, rows[1])

	assertHeaderContains(t, header, "1th_inferred_country_resolved_code")
	assertHeaderContains(t, header, "2th_inferred_country_resolved_code")
	if got := values["1th_inferred_country_resolved_code"]; got != "GB" {
		t.Fatalf("1th_inferred_country_resolved_code = %q, want GB", got)
	}
	if got := values["2th_inferred_country_resolved_code"]; got != "" {
		t.Fatalf("2th_inferred_country_resolved_code = %q, want empty", got)
	}
}

func TestWriteHumanReadableInferredCountryColumnsAreEmptyWithoutTownMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	result := sampleResult()
	result.FuzzyResult.TownMatches = nil

	rows := readHumanReadable(t, path, []core.Result{result}, true, false)
	header := rows[0]
	values := mapRow(header, rows[1])

	assertHeaderContains(t, header, "1th_inferred_country_resolved_code")
	assertHeaderContains(t, header, "2th_inferred_country_resolved_code")
	if got := values["1th_inferred_country_resolved_code"]; got != "" {
		t.Fatalf("1th_inferred_country_resolved_code = %q, want empty", got)
	}
	if got := values["2th_inferred_country_resolved_code"]; got != "" {
		t.Fatalf("2th_inferred_country_resolved_code = %q, want empty", got)
	}
}

func TestWriteJSONOmitsVerboseCRFTensorsWhenVerboseFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")

	if err := WriteJSON(path, []core.Result{sampleResult()}, false); err != nil {
		t.Fatalf("writing json: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading json: %v", err)
	}

	if strings.Contains(string(data), "emissions_per_tag") {
		t.Fatalf("non-verbose json contains emissions_per_tag: %s", data)
	}
	if strings.Contains(string(data), "log_probas_per_tag") {
		t.Fatalf("non-verbose json contains log_probas_per_tag: %s", data)
	}
	if !strings.Contains(string(data), "predictions_per_tag") {
		t.Fatalf("non-verbose json missing predictions_per_tag: %s", data)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshalling json: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("result count = %d, want 1", len(decoded))
	}
}

func TestWriteJSONIncludesVerboseCRFTensorsWhenVerboseTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")

	if err := WriteJSON(path, []core.Result{sampleResult()}, true); err != nil {
		t.Fatalf("writing json: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading json: %v", err)
	}

	if !strings.Contains(string(data), "emissions_per_tag") {
		t.Fatalf("verbose json missing emissions_per_tag: %s", data)
	}
	if !strings.Contains(string(data), "log_probas_per_tag") {
		t.Fatalf("verbose json missing log_probas_per_tag: %s", data)
	}
}

func mapRow(header, row []string) map[string]string {
	values := make(map[string]string, len(header))
	for i, column := range header {
		if i < len(row) {
			values[column] = row[i]
		}
	}
	return values
}

func readHumanReadable(t *testing.T, path string, results []core.Result, showInferredCountry bool, verbose bool) [][]string {
	t.Helper()
	if err := WriteHumanReadable(path, results, showInferredCountry, verbose); err != nil {
		t.Fatalf("writing human-readable output: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening human-readable output: %v", err)
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("reading human-readable output: %v", err)
	}
	if len(rows) != len(results)+1 {
		t.Fatalf("row count = %d, want %d", len(rows), len(results)+1)
	}
	return rows
}

func assertHeaderContains(t *testing.T, header []string, column string) {
	t.Helper()
	for _, got := range header {
		if got == column {
			return
		}
	}
	t.Fatalf("header %v does not contain %q", header, column)
}

func assertHeaderDoesNotContain(t *testing.T, header []string, column string) {
	t.Helper()
	for _, got := range header {
		if got == column {
			t.Fatalf("header %v contains %q", header, column)
		}
	}
}

func sampleResult() core.Result {
	return core.Result{
		CRFResult: core.CRFResult{
			Details: core.Details{
				Content:               "10 Downing Street\nLondon",
				CountryCode:           "GB",
				CountryCodeConfidence: 0.91,
				Spans: []core.TaggedSpan{
					{Start: 18, End: 24, Tag: core.TagTown},
				},
			},
			PredictionsPerTag: map[core.Tag][]core.PredictionCRF{
				core.TagTown: {
					{
						TaggedSpan: core.TaggedSpan{Start: 18, End: 24, Tag: core.TagTown},
						Confidence: 0.88,
						Prediction: "London",
					},
				},
			},
			EmissionsPerTag: map[core.Tag][]float64{
				core.TagTown: {1.25, 1.5},
			},
			LogProbasPerTag: map[core.Tag][]float64{
				core.TagTown: {-0.25, -0.5},
			},
		},
		FuzzyResult: core.FuzzyResult{
			CountryMatches: []core.FuzzyMatch{
				{
					Matched:    "United Kingdom\n",
					Origin:     "GB",
					FinalScore: 0.96123,
				},
				{
					Matched:    "France",
					Origin:     "FR",
					FinalScore: 0.61,
				},
			},
			TownMatches: []core.FuzzyMatch{
				{
					Matched:     "London",
					Possibility: "London",
					Origin:      "GB",
					FinalScore:  0.885,
				},
			},
		},
		IBANs:                 []string{"GB82WEST12345698765432"},
		SuggestedCountry:      "GB",
		HasSuggestedCountry:   true,
		ForceSuggestedCountry: true,
	}
}
