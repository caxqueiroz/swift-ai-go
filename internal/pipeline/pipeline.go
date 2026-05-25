package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/fuzzy"
	"github.com/tipmarket/swift-ai/internal/normalize"
	"github.com/tipmarket/swift-ai/internal/postcode"
	"github.com/tipmarket/swift-ai/internal/postprocess"
	"github.com/tipmarket/swift-ai/internal/resources"
)

const defaultBatchSize = 1024

type ModelRunner interface {
	TagBatch(ctx context.Context, data []string) ([]core.CRFResult, error)
}

type FuzzyRunner interface {
	MatchBatch(ctx context.Context, data []string, samples []core.AddressSample) ([]core.FuzzyResult, error)
}

type PostcodeRunner interface {
	MatchBatch(ctx context.Context, data []string) ([][]core.PostcodeMatch, error)
}

type PostprocessRunner interface {
	Run(crf []core.CRFResult, fuzzy []core.FuzzyResult, postcodes [][]core.PostcodeMatch, samples []core.AddressSample) ([]core.Result, error)
}

type Option func(*Pipeline)

type Pipeline struct {
	cfg               config.Config
	db                resources.Database
	modelRunner       ModelRunner
	fuzzyRunner       FuzzyRunner
	postcodeRunner    PostcodeRunner
	postprocessRunner PostprocessRunner
}

func New(cfg config.Config, db *resources.Database, modelRunner ModelRunner, opts ...Option) *Pipeline {
	database := resources.Database{}
	if db != nil {
		database = *db
	}

	p := &Pipeline{
		cfg:               cfg,
		db:                database,
		modelRunner:       modelRunner,
		fuzzyRunner:       newDefaultFuzzyRunner(cfg.Fuzzy, database),
		postcodeRunner:    newDefaultPostcodeRunner(database),
		postprocessRunner: postprocess.NewRunner(cfg, database),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

func WithFuzzyRunner(runner FuzzyRunner) Option {
	return func(p *Pipeline) {
		if runner != nil {
			p.fuzzyRunner = runner
		}
	}
}

func WithPostcodeRunner(runner PostcodeRunner) Option {
	return func(p *Pipeline) {
		if runner != nil {
			p.postcodeRunner = runner
		}
	}
}

func WithPostprocessRunner(runner PostprocessRunner) Option {
	return func(p *Pipeline) {
		if runner != nil {
			p.postprocessRunner = runner
		}
	}
}

func (p *Pipeline) Run(ctx context.Context, samples []core.AddressSample) ([]core.Result, error) {
	if err := p.validateDependencies(); err != nil {
		return nil, err
	}

	cleaned := make([]core.AddressSample, len(samples))
	for i, sample := range samples {
		cleanedSample, err := p.cleanAndValidateSample(sample)
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i, err)
		}
		cleaned[i] = cleanedSample
	}

	batchSize := p.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	results := make([]core.Result, 0, len(cleaned))
	for start := 0; start < len(cleaned); start += batchSize {
		end := min(start+batchSize, len(cleaned))
		batch := cleaned[start:end]
		texts := sampleTexts(batch)

		crfResults, err := p.modelRunner.TagBatch(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("tag batch starting at sample %d: %w", start, err)
		}
		if len(crfResults) != len(batch) {
			return nil, fmt.Errorf("tag batch starting at sample %d returned %d results, want %d", start, len(crfResults), len(batch))
		}

		fuzzyResults, err := p.fuzzyRunner.MatchBatch(ctx, texts, batch)
		if err != nil {
			return nil, fmt.Errorf("fuzzy match batch starting at sample %d: %w", start, err)
		}
		if len(fuzzyResults) != len(batch) {
			return nil, fmt.Errorf("fuzzy match batch starting at sample %d returned %d results, want %d", start, len(fuzzyResults), len(batch))
		}

		postcodeResults, err := p.postcodeRunner.MatchBatch(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("postcode match batch starting at sample %d: %w", start, err)
		}
		if len(postcodeResults) != len(batch) {
			return nil, fmt.Errorf("postcode match batch starting at sample %d returned %d results, want %d", start, len(postcodeResults), len(batch))
		}

		batchResults, err := p.postprocessRunner.Run(crfResults, fuzzyResults, postcodeResults, batch)
		if err != nil {
			return nil, fmt.Errorf("postprocess batch starting at sample %d: %w", start, err)
		}
		if len(batchResults) != len(batch) {
			return nil, fmt.Errorf("postprocess batch starting at sample %d returned %d results, want %d", start, len(batchResults), len(batch))
		}

		results = append(results, batchResults...)
	}

	return results, nil
}

func (p *Pipeline) validateDependencies() error {
	if p == nil {
		return errors.New("pipeline is nil")
	}
	if p.modelRunner == nil {
		return errors.New("pipeline model runner is nil")
	}
	if p.fuzzyRunner == nil {
		return errors.New("pipeline fuzzy runner is nil")
	}
	if p.postcodeRunner == nil {
		return errors.New("pipeline postcode runner is nil")
	}
	if p.postprocessRunner == nil {
		return errors.New("pipeline postprocess runner is nil")
	}
	return nil
}

func (p *Pipeline) cleanAndValidateSample(sample core.AddressSample) (core.AddressSample, error) {
	length := utf8.RuneCountInString(sample.Text)
	if length > p.cfg.CRF.MaxSequenceLength {
		return core.AddressSample{}, fmt.Errorf("address length %d exceeds max sequence length %d for %q",
			length, p.cfg.CRF.MaxSequenceLength, sample.Text)
	}

	cleaned := strings.ReplaceAll(sample.Text, `\n`, "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	cleaned = strings.ToUpper(cleaned)
	cleaned = normalize.DecodeAndClean(cleaned)
	cleaned = strings.ToUpper(cleaned)

	return core.AddressSample{
		Text:                  cleaned,
		SuggestedCountry:      sample.SuggestedCountry,
		HasSuggestedCountry:   sample.HasSuggestedCountry,
		ForceSuggestedCountry: sample.ForceSuggestedCountry,
	}, nil
}

func sampleTexts(samples []core.AddressSample) []string {
	texts := make([]string, len(samples))
	for i, sample := range samples {
		texts[i] = sample.Text
	}
	return texts
}

type defaultFuzzyRunner struct {
	cfg                   config.FuzzyConfig
	countryPossibilities  map[string][]string
	countryCodeCandidates map[string][]string
	townPossibilities     map[string][]string
}

func newDefaultFuzzyRunner(cfg config.FuzzyConfig, db resources.Database) defaultFuzzyRunner {
	return defaultFuzzyRunner{
		cfg:                   cfg,
		countryPossibilities:  cloneStringSliceMap(db.CountryPossibilities),
		countryCodeCandidates: buildCountryCodeCandidates(db),
		townPossibilities:     buildTownPossibilities(db.TownPossibilities),
	}
}

func (r defaultFuzzyRunner) MatchBatch(_ context.Context, data []string, _ []core.AddressSample) ([]core.FuzzyResult, error) {
	countryMatches := fuzzy.ScanAllBatched(data, r.countryPossibilities, r.cfg.ScoreCutoffCountry, r.cfg.ToleranceCountry)
	countryCodeMatches := fuzzy.ScanAllBatched(data, r.countryCodeCandidates, r.cfg.ScoreCutoffCountry, r.cfg.ToleranceCountry)
	townMatches := fuzzy.ScanAllBatched(data, r.townPossibilities, r.cfg.ScoreCutoffTown, r.cfg.ToleranceTown)

	results := make([]core.FuzzyResult, len(data))
	for i := range data {
		results[i] = core.FuzzyResult{
			CountryMatches:     countryMatches[i],
			CountryCodeMatches: countryCodeMatches[i],
			TownMatches:        townMatches[i],
		}
	}
	return results, nil
}

type defaultPostcodeRunner struct {
	entries map[string]map[string][]resources.PostcodeEntry
	specs   map[string]resources.CountrySpec
}

func newDefaultPostcodeRunner(db resources.Database) defaultPostcodeRunner {
	return defaultPostcodeRunner{
		entries: db.Postcodes,
		specs:   db.CountrySpecs,
	}
}

func (r defaultPostcodeRunner) MatchBatch(_ context.Context, data []string) ([][]core.PostcodeMatch, error) {
	results := make([][]core.PostcodeMatch, len(data))
	if len(r.entries) == 0 {
		return results, nil
	}

	for i, text := range data {
		for countryCode, entries := range r.entries {
			spec := r.specs[countryCode]
			if spec.PostalCodeRegex == "" {
				continue
			}

			matches, err := postcode.FindTownMatches(postcodeEntries(entries), []string{spec.PostalCodeRegex}, text, "")
			if err != nil {
				return nil, fmt.Errorf("match postcodes for country %s: %w", countryCode, err)
			}
			results[i] = append(results[i], matches...)
		}
	}

	return results, nil
}

func buildCountryCodeCandidates(db resources.Database) map[string][]string {
	candidates := make(map[string][]string)
	for code := range db.CountryAlpha2 {
		addCodeCandidate(candidates, code)
	}
	for _, origins := range db.CountryPossibilities {
		for _, origin := range origins {
			addCodeCandidate(candidates, origin)
		}
	}
	return candidates
}

func addCodeCandidate(candidates map[string][]string, code string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return
	}
	candidates[code] = []string{code}
}

func buildTownPossibilities(townsByCountry map[string]map[string]struct{}) map[string][]string {
	possibilities := make(map[string][]string)
	for countryCode, aliases := range townsByCountry {
		code := strings.ToUpper(strings.TrimSpace(countryCode))
		if code == "" {
			continue
		}
		for alias := range aliases {
			alias = strings.ToUpper(alias)
			if alias == "" {
				continue
			}
			possibilities[alias] = append(possibilities[alias], code)
		}
	}
	return possibilities
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(values))
	for key, value := range values {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}

func postcodeEntries(entries map[string][]resources.PostcodeEntry) map[string][]postcode.Entry {
	converted := make(map[string][]postcode.Entry, len(entries))
	for code, values := range entries {
		converted[code] = make([]postcode.Entry, len(values))
		for i, value := range values {
			converted[code][i] = postcode.Entry{
				Town:   value.Town,
				Origin: value.Origin,
			}
		}
	}
	return converted
}
