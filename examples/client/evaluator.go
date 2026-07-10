package main

import (
	"sort"
	"strings"
	"unicode"

	"github.com/mozillazg/go-unidecode"
	"github.com/tipmarket/swift-ai/internal/holdout"
)

const (
	commonTownMin = 1000
	mediumTownMin = 100
)

const (
	inputModeAddress            = "address"
	inputModeAddressTown        = "address-town"
	inputModeAddressTownCountry = "address-town-country"
)

type townGroup struct {
	Country string
	Town    string
	Count   int
}

type samplePlan struct {
	Country string
	Town    string
	Stratum string
	Offset  int
}

type randomSource interface {
	IntN(n int) int
}

func planSamples(groups []townGroup, limit int, rng randomSource) []samplePlan {
	if limit <= 0 || len(groups) == 0 {
		return nil
	}
	byStratum := map[string][]townGroup{}
	for _, group := range groups {
		group.Country = canonicalCountry(group.Country)
		group.Town = strings.TrimSpace(group.Town)
		if group.Country == "" || group.Town == "" || group.Count <= 0 {
			continue
		}
		stratum := stratumName(group.Country, group.Count)
		byStratum[stratum] = append(byStratum[stratum], group)
	}
	if len(byStratum) == 0 {
		return nil
	}

	strata := make([]string, 0, len(byStratum))
	for stratum := range byStratum {
		strata = append(strata, stratum)
	}
	sort.Strings(strata)

	base := limit / len(strata)
	remainder := limit % len(strata)
	plans := make([]samplePlan, 0, limit)
	for i, stratum := range strata {
		count := base
		if i < remainder {
			count++
		}
		if count == 0 {
			continue
		}
		groups := byStratum[stratum]
		for range count {
			group := groups[rng.IntN(len(groups))]
			plans = append(plans, samplePlan{
				Country: group.Country,
				Town:    group.Town,
				Stratum: stratum,
				Offset:  rng.IntN(group.Count),
			})
		}
	}
	return plans
}

func stratumName(country string, count int) string {
	switch {
	case count >= commonTownMin:
		return country + ":common"
	case count >= mediumTownMin:
		return country + ":medium"
	default:
		return country + ":rare"
	}
}

type evaluationRow struct {
	ID                       string  `json:"id,omitempty"`
	Address                  string  `json:"address,omitempty"`
	InputMode                string  `json:"input_mode,omitempty"`
	Stratum                  string  `json:"stratum"`
	ExpectedCountry          string  `json:"expected_country"`
	ExpectedCountryCanonical string  `json:"expected_country_canonical"`
	ExpectedTown             string  `json:"expected_town"`
	ExpectedTownCanonical    string  `json:"expected_town_canonical"`
	ActualCountry            string  `json:"actual_country"`
	ActualCountryCanonical   string  `json:"actual_country_canonical"`
	ActualTown               string  `json:"actual_town"`
	ActualTownCanonical      string  `json:"actual_town_canonical"`
	CountryCorrect           bool    `json:"country_correct"`
	TownCorrect              bool    `json:"town_correct"`
	BothCorrect              bool    `json:"both_correct"`
	Committed                bool    `json:"committed"`
	ActualCountryConfidence  float64 `json:"actual_country_confidence,omitempty"`
	ActualTownConfidence     float64 `json:"actual_town_confidence,omitempty"`
	ServedBy                 string  `json:"served_by,omitempty"`
	Status                   string  `json:"status,omitempty"`
	Band                     string  `json:"band,omitempty"`
	QualityScore             float64 `json:"quality_score,omitempty"`
	QualityReason            string  `json:"quality_reason,omitempty"`
}

type metricRecorder struct {
	overall metricSummary
	strata  map[string]*metricSummary
}

type metricReport struct {
	metricSummary
	Strata map[string]metricSummary `json:"strata"`
}

type metricSummary struct {
	Total                       int     `json:"total"`
	CountryCorrect              int     `json:"country_correct"`
	TownCorrect                 int     `json:"town_correct"`
	BothCorrect                 int     `json:"both_correct"`
	Committed                   int     `json:"committed"`
	CommittedCountryCorrect     int     `json:"committed_country_correct"`
	CommittedTownCorrect        int     `json:"committed_town_correct"`
	CommittedBothCorrect        int     `json:"committed_both_correct"`
	CountryAccuracy             float64 `json:"country_accuracy"`
	TownAccuracy                float64 `json:"town_accuracy"`
	BothAccuracy                float64 `json:"both_accuracy"`
	Coverage                    float64 `json:"coverage"`
	PrecisionAtCommittedCountry float64 `json:"precision_at_committed_country"`
	PrecisionAtCommittedTown    float64 `json:"precision_at_committed_town"`
	PrecisionAtCommittedBoth    float64 `json:"precision_at_committed_both"`
	FalsePositiveRateCommitted  float64 `json:"false_positive_rate_committed"`
}

func newMetricRecorder() *metricRecorder {
	return &metricRecorder{strata: map[string]*metricSummary{}}
}

func (r *metricRecorder) Add(row evaluationRow) {
	row = finalizeEvaluationRow(row)
	countryOK := row.CountryCorrect
	townOK := row.TownCorrect
	committed := row.Committed
	r.addTo(&r.overall, countryOK, townOK, committed)
	stratum := row.Stratum
	if stratum == "" {
		stratum = "unknown"
	}
	if r.strata[stratum] == nil {
		r.strata[stratum] = &metricSummary{}
	}
	r.addTo(r.strata[stratum], countryOK, townOK, committed)
}

func (r *metricRecorder) addTo(summary *metricSummary, countryOK bool, townOK bool, committed bool) {
	summary.Total++
	if countryOK {
		summary.CountryCorrect++
	}
	if townOK {
		summary.TownCorrect++
	}
	if countryOK && townOK {
		summary.BothCorrect++
	}
	if committed {
		summary.Committed++
		if countryOK {
			summary.CommittedCountryCorrect++
		}
		if townOK {
			summary.CommittedTownCorrect++
		}
		if countryOK && townOK {
			summary.CommittedBothCorrect++
		}
	}
	refreshRates(summary)
}

func (r *metricRecorder) Report() metricReport {
	strata := make(map[string]metricSummary, len(r.strata))
	for key, summary := range r.strata {
		copied := *summary
		strata[key] = copied
	}
	return metricReport{metricSummary: r.overall, Strata: strata}
}

func refreshRates(summary *metricSummary) {
	if summary.Total == 0 {
		return
	}
	total := float64(summary.Total)
	summary.CountryAccuracy = float64(summary.CountryCorrect) / total
	summary.TownAccuracy = float64(summary.TownCorrect) / total
	summary.BothAccuracy = float64(summary.BothCorrect) / total
	summary.Coverage = float64(summary.Committed) / total
	if summary.Committed == 0 {
		return
	}
	committed := float64(summary.Committed)
	summary.PrecisionAtCommittedCountry = float64(summary.CommittedCountryCorrect) / committed
	summary.PrecisionAtCommittedTown = float64(summary.CommittedTownCorrect) / committed
	summary.PrecisionAtCommittedBoth = float64(summary.CommittedBothCorrect) / committed
	summary.FalsePositiveRateCommitted = 1 - summary.PrecisionAtCommittedBoth
}

func canonicalCountry(country string) string {
	return strings.ToUpper(strings.TrimSpace(country))
}

func canonicalTown(town string) string {
	town = strings.ToUpper(unidecode.Unidecode(strings.TrimSpace(town)))
	var out strings.Builder
	lastSpace := true
	for _, r := range town {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				out.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func isMismatch(row evaluationRow) bool {
	row = finalizeEvaluationRow(row)
	return !row.BothCorrect
}

func isCommitted(row evaluationRow) bool {
	return strings.EqualFold(strings.TrimSpace(row.Status), "resolved") &&
		strings.EqualFold(strings.TrimSpace(row.Band), "high")
}

func finalizeEvaluationRow(row evaluationRow) evaluationRow {
	row.ExpectedCountryCanonical = canonicalCountry(row.ExpectedCountry)
	row.ActualCountryCanonical = canonicalCountry(row.ActualCountry)
	row.ExpectedTownCanonical = canonicalTown(row.ExpectedTown)
	row.ActualTownCanonical = canonicalTown(row.ActualTown)
	row.CountryCorrect = row.ExpectedCountryCanonical == row.ActualCountryCanonical
	row.TownCorrect = row.ExpectedTownCanonical == row.ActualTownCanonical
	row.BothCorrect = row.CountryCorrect && row.TownCorrect
	row.Committed = isCommitted(row)
	return row
}

func shouldLogRow(row evaluationRow, logAll bool) bool {
	return logAll || isMismatch(row)
}

func applyHoldoutFilter(records []addressRecord, filter holdout.Filter, limit int) []addressRecord {
	if !filter.Enabled() {
		return records
	}
	filtered := make([]addressRecord, 0, min(len(records), limit))
	for _, record := range records {
		if !filter.Include(record.ID) {
			continue
		}
		filtered = append(filtered, record)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func buildInputText(record addressRecord, mode string) string {
	lines := []string{strings.TrimSpace(record.Address)}
	switch mode {
	case inputModeAddress:
	case inputModeAddressTownCountry:
		lines = append(lines, strings.TrimSpace(record.Town), canonicalCountry(record.Country))
	case inputModeAddressTown, "":
		lines = append(lines, strings.TrimSpace(record.Town))
	default:
		lines = append(lines, strings.TrimSpace(record.Town))
	}

	compact := lines[:0]
	for _, line := range lines {
		if line != "" {
			compact = append(compact, line)
		}
	}
	return strings.Join(compact, "\n")
}
