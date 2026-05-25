package core

type Tag string

const (
	TagOther                Tag = "OTHER"
	TagCountry              Tag = "COUNTRY"
	TagTown                 Tag = "TOWN"
	TagStreet               Tag = "STREET"
	TagPostalCode           Tag = "POSTAL_CODE"
	TagContinent            Tag = "CONTINENT"
	TagGenericWord          Tag = "GENERIC_WORD"
	TagNaturalPersonName    Tag = "NATURAL_PERSON_NAME"
	TagFinancialInstitution Tag = "FINANCIAL_INSTITUTION"
	TagBusinessEntityName   Tag = "BUSINESS_ENTITY_NAME"
	TagBusinessEntityType   Tag = "BUSINESS_ENTITY_TYPE"
	TagMonth                Tag = "MONTH"
	TagYear                 Tag = "YEAR"
	TagIBAN                 Tag = "IBAN"
	TagFloat                Tag = "FLOAT"
	TagInteger              Tag = "INTEGER"
	TagAlphanumeric         Tag = "ALPHANUMERIC"
	TagCurrency             Tag = "CURRENCY"
	TagFinancialJargon      Tag = "FINANCIAL_JARGON"
	TagSeparator            Tag = "SEPARATOR"
	TagHouseNumber          Tag = "HOUSE_NUMBER"
	TagDate                 Tag = "DATE"
	TagSpecifier            Tag = "SPECIFIER"
	TagPhoneNumber          Tag = "PHONE_NUMBER"
)

func (t Tag) String() string {
	return string(t)
}

type BIO string

const (
	BioBefore BIO = "B-"
	BioInside BIO = "I-"
	BioOther  BIO = "OTHER"
)

func (b BIO) String() string {
	return string(b)
}

type BIOTag struct {
	Tag Tag `json:"tag"`
	BIO BIO `json:"bio"`
}

type TaggedSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
	Tag   Tag `json:"tag"`
}

type Details struct {
	Content               string       `json:"content"`
	CountryCode           string       `json:"country_code,omitempty"`
	CountryCodeConfidence float64      `json:"country_code_confidence,omitempty"`
	Spans                 []TaggedSpan `json:"spans"`
}

type AddressSample struct {
	Text                  string
	SuggestedCountry      string
	HasSuggestedCountry   bool
	ForceSuggestedCountry bool
}

type Flag string

type FuzzyMatch struct {
	Start            int     `json:"start"`
	End              int     `json:"end"`
	Matched          string  `json:"matched"`
	Possibility      string  `json:"possibility"`
	Dist             int     `json:"dist"`
	Flags            []Flag  `json:"flags,omitempty"`
	Origin           string  `json:"origin,omitempty"`
	CRFScore         float64 `json:"crf_score,omitempty"`
	TransformerScore float64 `json:"transformer_score,omitempty"`
	FinalScore       float64 `json:"final_score,omitempty"`
}

type PredictionCRF struct {
	TaggedSpan
	Confidence float64 `json:"confidence"`
	Prediction string  `json:"prediction"`
}

type CRFResult struct {
	Details           Details
	PredictionsPerTag map[Tag][]PredictionCRF
	EmissionsPerTag   map[Tag][]float64
	LogProbasPerTag   map[Tag][]float64
}

type FuzzyResult struct {
	CountryMatches      []FuzzyMatch
	CountryCodeMatches  []FuzzyMatch
	TownMatches         []FuzzyMatch
	ExtendedTownMatches []FuzzyMatch
}

type PostcodeMatch struct {
	Start       int
	End         int
	Matched     string
	Possibility string
	Origin      string
}

type Result struct {
	CRFResult             CRFResult
	FuzzyResult           FuzzyResult
	IBANs                 []string
	SuggestedCountry      string
	HasSuggestedCountry   bool
	ForceSuggestedCountry bool
}
