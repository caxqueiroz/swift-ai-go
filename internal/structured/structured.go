package structured

import (
	"sort"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

type Address struct {
	AddressLine       string  `json:"address_line"`
	Country           string  `json:"country,omitempty"`
	Town              string  `json:"town,omitempty"`
	PostalCode        string  `json:"postal_code,omitempty"`
	Street            string  `json:"street,omitempty"`
	CountryConfidence float64 `json:"country_confidence,omitempty"`
	TownConfidence    float64 `json:"town_confidence,omitempty"`
}

func FromResult(result core.Result) Address {
	address := Address{
		AddressLine: result.CRFResult.Details.Content,
		PostalCode:  bestPrediction(result.CRFResult.PredictionsPerTag[core.TagPostalCode]),
		Street:      joinedPredictions(result.CRFResult.PredictionsPerTag[core.TagStreet]),
	}

	if country, ok := bestCountry(result.FuzzyResult.CountryMatches); ok {
		address.Country = country.Origin
		address.CountryConfidence = country.FinalScore
	}
	if town, ok := bestTown(result.FuzzyResult.TownMatches); ok {
		address.Town = town.Possibility
		address.TownConfidence = town.FinalScore
	}

	return address
}

func bestCountry(matches []core.FuzzyMatch) (core.FuzzyMatch, bool) {
	for _, match := range matches {
		if match.Origin == "" || match.Origin == "NO COUNTRY" || match.Possibility == "NO COUNTRY" {
			continue
		}
		return match, true
	}
	return core.FuzzyMatch{}, false
}

func bestTown(matches []core.FuzzyMatch) (core.FuzzyMatch, bool) {
	for _, match := range matches {
		if match.Possibility == "" || match.Possibility == "NO TOWN" || match.Origin == "NO TOWN" {
			continue
		}
		return match, true
	}
	return core.FuzzyMatch{}, false
}

func bestPrediction(predictions []core.PredictionCRF) string {
	if len(predictions) == 0 {
		return ""
	}
	best := predictions[0]
	for _, prediction := range predictions[1:] {
		if prediction.Confidence > best.Confidence {
			best = prediction
		}
	}
	return strings.TrimSpace(best.Prediction)
}

func joinedPredictions(predictions []core.PredictionCRF) string {
	if len(predictions) == 0 {
		return ""
	}
	ordered := append([]core.PredictionCRF(nil), predictions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Start < ordered[j].Start
	})

	parts := make([]string, 0, len(ordered))
	for _, prediction := range ordered {
		value := strings.TrimSpace(prediction.Prediction)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}
