package quality

import "github.com/tipmarket/swift-ai/internal/structured"

type Status string

const (
	StatusResolved    Status = "resolved"
	StatusPartial     Status = "partial"
	StatusNeedsReview Status = "needs_review"
)

type Band string

const (
	BandHigh   Band = "high"
	BandMedium Band = "medium"
	BandLow    Band = "low"
)

type Reason string

const (
	ReasonHighConfidence      Reason = "high_confidence"
	ReasonConfidenceBelowHigh Reason = "confidence_below_high"
	ReasonLowConfidence       Reason = "low_confidence"
	ReasonMissingCountry      Reason = "missing_country"
	ReasonMissingTown         Reason = "missing_town"
)

type Thresholds struct {
	High   float64
	Medium float64
}

type Assessment struct {
	Status Status
	Band   Band
	Score  float64
	Reason Reason
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		High:   0.95,
		Medium: 0.80,
	}
}

func Assess(address structured.Address, thresholds Thresholds) Assessment {
	thresholds = normalizeThresholds(thresholds)
	hasCountry := address.Country != ""
	hasTown := address.Town != ""

	switch {
	case hasCountry && hasTown:
		score := min(address.CountryConfidence, address.TownConfidence)
		switch {
		case score >= thresholds.High:
			return Assessment{Status: StatusResolved, Band: BandHigh, Score: score, Reason: ReasonHighConfidence}
		case score >= thresholds.Medium:
			return Assessment{Status: StatusNeedsReview, Band: BandMedium, Score: score, Reason: ReasonConfidenceBelowHigh}
		default:
			return Assessment{Status: StatusNeedsReview, Band: BandLow, Score: score, Reason: ReasonLowConfidence}
		}
	case hasCountry:
		return partial(address.CountryConfidence, ReasonMissingTown, thresholds)
	case hasTown:
		return partial(address.TownConfidence, ReasonMissingCountry, thresholds)
	default:
		return Assessment{Status: StatusNeedsReview, Band: BandLow, Reason: ReasonMissingCountry}
	}
}

func partial(score float64, reason Reason, thresholds Thresholds) Assessment {
	band := BandLow
	if score >= thresholds.Medium {
		band = BandMedium
	}
	return Assessment{Status: StatusPartial, Band: band, Score: score, Reason: reason}
}

func normalizeThresholds(thresholds Thresholds) Thresholds {
	defaults := DefaultThresholds()
	if thresholds.High <= 0 {
		thresholds.High = defaults.High
	}
	if thresholds.Medium <= 0 {
		thresholds.Medium = defaults.Medium
	}
	if thresholds.Medium > thresholds.High {
		thresholds.Medium = thresholds.High
	}
	return thresholds
}
