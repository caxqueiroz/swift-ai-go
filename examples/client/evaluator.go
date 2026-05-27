package main

import (
	"sort"
	"strings"
	"unicode"

	"github.com/mozillazg/go-unidecode"
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
	ID              string `json:"id,omitempty"`
	Address         string `json:"address,omitempty"`
	Stratum         string `json:"stratum"`
	ExpectedCountry string `json:"expected_country"`
	ExpectedTown    string `json:"expected_town"`
	ActualCountry   string `json:"actual_country"`
	ActualTown      string `json:"actual_town"`
	ServedBy        string `json:"served_by,omitempty"`
	Status          string `json:"status,omitempty"`
	Band            string `json:"band,omitempty"`
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
	Total           int     `json:"total"`
	CountryCorrect  int     `json:"country_correct"`
	TownCorrect     int     `json:"town_correct"`
	BothCorrect     int     `json:"both_correct"`
	CountryAccuracy float64 `json:"country_accuracy"`
	TownAccuracy    float64 `json:"town_accuracy"`
	BothAccuracy    float64 `json:"both_accuracy"`
}

func newMetricRecorder() *metricRecorder {
	return &metricRecorder{strata: map[string]*metricSummary{}}
}

func (r *metricRecorder) Add(row evaluationRow) {
	countryOK := canonicalCountry(row.ExpectedCountry) == canonicalCountry(row.ActualCountry)
	townOK := canonicalTown(row.ExpectedTown) == canonicalTown(row.ActualTown)
	r.addTo(&r.overall, countryOK, townOK)
	stratum := row.Stratum
	if stratum == "" {
		stratum = "unknown"
	}
	if r.strata[stratum] == nil {
		r.strata[stratum] = &metricSummary{}
	}
	r.addTo(r.strata[stratum], countryOK, townOK)
}

func (r *metricRecorder) addTo(summary *metricSummary, countryOK bool, townOK bool) {
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
	return canonicalCountry(row.ExpectedCountry) != canonicalCountry(row.ActualCountry) ||
		canonicalTown(row.ExpectedTown) != canonicalTown(row.ActualTown)
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
