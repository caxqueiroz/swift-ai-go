package config

import (
	"reflect"
	"testing"
)

func TestDefaultReturnsPipelineDefaults(t *testing.T) {
	want := Config{
		BatchSize: 1024,
		Database: DatabaseConfig{
			PrefixFolderPath:       "resources",
			GeonamesParquet:        "towns_all_countries.parquet",
			TownAliases:            "town_aliases.json",
			CountryAliases:         "country_names.json",
			CountryProvinceAliases: "country_province_names.json",
			CountryTownSameName:    "misc/country_city_same_name.json",
			CountryGroupings:       "misc/country_groupings_with_iso_code.json",
			CountrySpecs:           "misc/country_specs.json",
			TownMinimalPopulation:  500,
			EnableOSMData:          false,
			TownEntitiesOSM:        "cities_osm_cleaned.parquet",
		},
		Fuzzy: FuzzyConfig{
			ScoreCutoffCountry: 80,
			ToleranceCountry:   1,
			ScoreCutoffTown:    80,
			ToleranceTown:      1,
		},
		CRF: CRFConfig{
			ModelPath:         "resources/models/address_transformer.onnx",
			ModelConfigPath:   "resources/models/address_transformer.config.json",
			CRFConfigPath:     "resources/models/address_crf.json",
			MaxSequenceLength: 224,
			Device:            "cpu",
		},
		PostProcessing: PostProcessingConfig{
			MinimalFinalScoreCountry:     0.15,
			MinimalFinalScoreTown:        0.15,
			IBANPattern:                  `(?=([A-Z]{2}\d{2}(?:[ ]?[A-Z0-9]{4}){1,7}))`,
			BaseScoreSuggestedCountry:    0.95,
			IsMetropolisThreshold:        1_000_000,
			IsSmallTownThreshold:         12_000,
			PartOfStreetRatio:            0.50,
			ShowInferredCountry:          true,
			NoTownFoundMul:               0.7,
			NoCountryFoundMul:            0.1,
			CountriesWithCommonProvinces: []string{"CN", "US"},
		},
		TownWeights:    expectedTownWeights(),
		CountryWeights: expectedCountryWeights(),
	}

	assertEqual(t, "Default()", Default(), want)
}

func TestDefaultTownWeightsReturnsAllDefaults(t *testing.T) {
	assertEqual(t, "DefaultTownWeights()", DefaultTownWeights(), expectedTownWeights())
}

func TestDefaultCountryWeightsReturnsAllDefaults(t *testing.T) {
	assertEqual(t, "DefaultCountryWeights()", DefaultCountryWeights(), expectedCountryWeights())
}

func assertEqual[T any](t *testing.T, name string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func expectedTownWeights() TownWeights {
	return TownWeights{
		IsInLastThird:                    0.01,
		CouldBeReasonableMistake:         0.25,
		CountryIsPresentBonus:            0.15,
		SuggestedCountryIsPresentBonus:   0.65,
		MLPCountryIsPresentBonus:         0.05,
		IsVeryCloseToCountry:             0.30,
		IsOnSameLineAsCountry:            0.15,
		PostcodeForTownFound:             0.4,
		IsMetropolis:                     0.10,
		IsAloneOnLine:                    0.20,
		ContainsTypo:                     -0.85,
		IsInsideAnotherWord:              -0.65,
		IsInFirstThird:                   -0.01,
		IsShort:                          -0.25,
		IsInsideAnotherLowerRankedMatch:  -0.30,
		IsSmallTown:                      -0.20,
		IsSmallTownAndCountryNotPresent:  -0.30,
		CountryIsPresentMalus:            0.10,
		IsFromExtendedData:               -0.15,
		IsNotLargestTownWithName:         -0.10,
		IsInsideStreet:                   -0.20,
		IsCommonStateProvinceAlias:       -0.10,
		IsUncommonStateProvinceAlias:     -0.15,
		IsShortAndNonzeroDistScore:       -2.00,
		IsShortAndIsInsideAnotherWord:    -2.00,
		IsInsideAnotherHigherRankedMatch: -2.00,
	}
}

func expectedCountryWeights() CountryWeights {
	return CountryWeights{
		IsInLastThird:                    0.01,
		CouldBeReasonableMistake:         0.10,
		TownIsPresent:                    0.20,
		IsVeryCloseToTown:                0.20,
		IsOnSameLineAsTown:               0.10,
		PostalCodeIsPresent:              0.10,
		IBANIsPresent:                    0.10,
		PhonePrefixIsPresent:             0.10,
		DomainIsPresent:                  0.10,
		MLPStronglyAgrees:                0.20,
		MLPAgrees:                        0.15,
		MLPDoesntDisagree:                0.05,
		ContainsTypo:                     -0.50,
		IsInsideAnotherWord:              -0.60,
		IsInFirstThird:                   -0.01,
		IsShort:                          -0.05,
		IsInsideAnotherLowerRankedMatch:  -0.30,
		IsInsideStreet:                   -0.20,
		IsCommonStateProvinceAlias:       -0.10,
		IsUncommonStateProvinceAlias:     -0.15,
		IsShortAndNonzeroDistScore:       -2.00,
		IsShortAndIsInsideAnotherWord:    -2.00,
		IsInsideAnotherHigherRankedMatch: -2.00,
	}
}
