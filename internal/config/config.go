package config

type Config struct {
	BatchSize      int
	Database       DatabaseConfig
	Fuzzy          FuzzyConfig
	CRF            CRFConfig
	PostProcessing PostProcessingConfig
	TownWeights    TownWeights
	CountryWeights CountryWeights
}

type DatabaseConfig struct {
	PrefixFolderPath       string
	GeonamesParquet        string
	TownAliases            string
	CountryAliases         string
	CountryProvinceAliases string
	CountryTownSameName    string
	CountryGroupings       string
	CountrySpecs           string
	TownMinimalPopulation  int
	EnableOSMData          bool
	TownEntitiesOSM        string
}

type FuzzyConfig struct {
	ScoreCutoffCountry int
	ToleranceCountry   int
	ScoreCutoffTown    int
	ToleranceTown      int
}

type CRFConfig struct {
	ModelPath         string
	ModelConfigPath   string
	CRFConfigPath     string
	MaxSequenceLength int
	Device            string
}

type PostProcessingConfig struct {
	MinimalFinalScoreCountry     float64
	MinimalFinalScoreTown        float64
	IBANPattern                  string
	BaseScoreSuggestedCountry    float64
	IsMetropolisThreshold        int
	IsSmallTownThreshold         int
	PartOfStreetRatio            float64
	ShowInferredCountry          bool
	NoTownFoundMul               float64
	NoCountryFoundMul            float64
	CountriesWithCommonProvinces []string
}

type TownWeights struct {
	IsInLastThird                    float64
	CouldBeReasonableMistake         float64
	CountryIsPresentBonus            float64
	SuggestedCountryIsPresentBonus   float64
	MLPCountryIsPresentBonus         float64
	IsVeryCloseToCountry             float64
	IsOnSameLineAsCountry            float64
	PostcodeForTownFound             float64
	IsMetropolis                     float64
	IsAloneOnLine                    float64
	ContainsTypo                     float64
	IsInsideAnotherWord              float64
	IsInFirstThird                   float64
	IsShort                          float64
	IsInsideAnotherLowerRankedMatch  float64
	IsSmallTown                      float64
	IsSmallTownAndCountryNotPresent  float64
	CountryIsPresentMalus            float64
	IsFromExtendedData               float64
	IsNotLargestTownWithName         float64
	IsInsideStreet                   float64
	IsCommonStateProvinceAlias       float64
	IsUncommonStateProvinceAlias     float64
	IsShortAndNonzeroDistScore       float64
	IsShortAndIsInsideAnotherWord    float64
	IsInsideAnotherHigherRankedMatch float64
}

type CountryWeights struct {
	IsInLastThird                    float64
	CouldBeReasonableMistake         float64
	TownIsPresent                    float64
	IsVeryCloseToTown                float64
	IsOnSameLineAsTown               float64
	PostalCodeIsPresent              float64
	IBANIsPresent                    float64
	PhonePrefixIsPresent             float64
	DomainIsPresent                  float64
	MLPStronglyAgrees                float64
	MLPAgrees                        float64
	MLPDoesntDisagree                float64
	ContainsTypo                     float64
	IsInsideAnotherWord              float64
	IsInFirstThird                   float64
	IsShort                          float64
	IsInsideAnotherLowerRankedMatch  float64
	IsInsideStreet                   float64
	IsCommonStateProvinceAlias       float64
	IsUncommonStateProvinceAlias     float64
	IsShortAndNonzeroDistScore       float64
	IsShortAndIsInsideAnotherWord    float64
	IsInsideAnotherHigherRankedMatch float64
}

func Default() Config {
	return Config{
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
		TownWeights:    DefaultTownWeights(),
		CountryWeights: DefaultCountryWeights(),
	}
}

func DefaultTownWeights() TownWeights {
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

func DefaultCountryWeights() CountryWeights {
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
