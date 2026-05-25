package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/tipmarket/swift-ai/internal/core"
)

type InferenceEngine interface {
	Run(ctx context.Context, tokenIDs [][]int64, mask [][]bool) (emissions [][][]float64, countryLogits [][]float64, err error)
}

type RunnerConfig struct {
	MaxSequenceLength  int
	BIOTagsToKeep      []core.BIOTag
	TagsToKeep         []core.Tag
	IDToCountry        map[int]string
	StrictBeforeInside bool
}

type Runner struct {
	tokenizer          *CharacterTokenizer
	engine             InferenceEngine
	crf                CRF
	maxSequenceLength  int
	bioTagsToKeep      []core.BIOTag
	tagsToKeep         []core.Tag
	idToCountry        map[int]string
	strictBeforeInside bool
}

func NewRunner(tokenizer *CharacterTokenizer, engine InferenceEngine, crf CRF, cfg RunnerConfig) (*Runner, error) {
	if tokenizer == nil {
		return nil, errors.New("tokenizer is nil")
	}
	if engine == nil {
		return nil, errors.New("inference engine is nil")
	}
	if cfg.MaxSequenceLength <= 0 {
		return nil, fmt.Errorf("max sequence length must be positive: %d", cfg.MaxSequenceLength)
	}
	if len(cfg.BIOTagsToKeep) == 0 {
		return nil, errors.New("BIO tags to keep is empty")
	}

	bioTagsToKeep := append([]core.BIOTag(nil), cfg.BIOTagsToKeep...)
	tagsToKeep := append([]core.Tag(nil), cfg.TagsToKeep...)
	idToCountry := make(map[int]string, len(cfg.IDToCountry))
	for id, country := range cfg.IDToCountry {
		idToCountry[id] = country
	}

	return &Runner{
		tokenizer:          tokenizer,
		engine:             engine,
		crf:                crf,
		maxSequenceLength:  cfg.MaxSequenceLength,
		bioTagsToKeep:      bioTagsToKeep,
		tagsToKeep:         tagsToKeep,
		idToCountry:        idToCountry,
		strictBeforeInside: cfg.StrictBeforeInside,
	}, nil
}

func (r *Runner) Tag(ctx context.Context, raw string) (core.CRFResult, error) {
	results, err := r.TagBatch(ctx, []string{raw})
	if err != nil {
		return core.CRFResult{}, err
	}
	if len(results) != 1 {
		return core.CRFResult{}, fmt.Errorf("tag batch returned %d results, want 1", len(results))
	}
	return results[0], nil
}

func (r *Runner) TagBatch(ctx context.Context, data []string) ([]core.CRFResult, error) {
	tokenIDs, mask := r.encodeBatch(data)
	emissions, countryLogits, err := r.engine.Run(ctx, tokenIDs, mask)
	if err != nil {
		return nil, fmt.Errorf("running inference: %w", err)
	}
	if err := r.validateOutputs(data, emissions, countryLogits); err != nil {
		return nil, err
	}

	decoded, err := r.decode(emissions, mask)
	if err != nil {
		return nil, err
	}
	marginals, err := r.marginalProbabilities(emissions, mask)
	if err != nil {
		return nil, err
	}

	results := make([]core.CRFResult, len(data))
	for batchIdx, raw := range data {
		country, confidence := r.countryPrediction(countryLogits, batchIdx)
		bioTags, err := r.pathToBIOTags(decoded[batchIdx])
		if err != nil {
			return nil, fmt.Errorf("converting decoded tags for batch %d: %w", batchIdx, err)
		}

		details := CreateDetailsFromBIOTags(raw, country, confidence, bioTags, r.strictBeforeInside)
		result := core.CRFResult{
			Details:           details,
			PredictionsPerTag: make(map[core.Tag][]core.PredictionCRF),
			EmissionsPerTag:   make(map[core.Tag][]float64),
			LogProbasPerTag:   make(map[core.Tag][]float64),
		}

		seqLen := len(emissions[batchIdx])
		for _, tag := range r.tagsToKeep {
			result.PredictionsPerTag[tag] = []core.PredictionCRF{}
			result.EmissionsPerTag[tag] = make([]float64, seqLen)
			result.LogProbasPerTag[tag] = make([]float64, seqLen)
		}

		computedSeries := make(map[core.Tag]bool)
		for _, span := range details.Spans {
			if _, ok := result.PredictionsPerTag[span.Tag]; !ok {
				result.PredictionsPerTag[span.Tag] = []core.PredictionCRF{}
			}
			if !computedSeries[span.Tag] {
				result.LogProbasPerTag[span.Tag], result.EmissionsPerTag[span.Tag] = r.tagSeries(span.Tag, emissions[batchIdx], marginals, batchIdx)
				computedSeries[span.Tag] = true
			}
			logProbas := result.LogProbasPerTag[span.Tag]
			result.PredictionsPerTag[span.Tag] = append(result.PredictionsPerTag[span.Tag], core.PredictionCRF{
				TaggedSpan: span,
				Confidence: meanSlice(logProbas, span.Start, span.End),
				Prediction: sliceRunes(raw, span.Start, span.End),
			})
		}

		results[batchIdx] = result
	}
	return results, nil
}

func (r *Runner) encodeBatch(data []string) ([][]int64, [][]bool) {
	tokenIDs := make([][]int64, len(data))
	mask := make([][]bool, len(data))
	for i, raw := range data {
		encoded := r.tokenizer.Encode(raw)
		tokenIDs[i] = make([]int64, r.maxSequenceLength)
		mask[i] = make([]bool, r.maxSequenceLength)
		for seq := range tokenIDs[i] {
			tokenIDs[i][seq] = int64(r.tokenizer.PadIndex())
		}
		limit := min(len(encoded), r.maxSequenceLength)
		for seq := 0; seq < limit; seq++ {
			tokenIDs[i][seq] = int64(encoded[seq])
			mask[i][seq] = true
		}
	}
	return tokenIDs, mask
}

func (r *Runner) validateOutputs(data []string, emissions [][][]float64, countryLogits [][]float64) error {
	if len(emissions) != len(data) {
		return fmt.Errorf("inference emissions batch size %d does not match input batch size %d", len(emissions), len(data))
	}
	if countryLogits != nil && len(countryLogits) != 0 && len(countryLogits) != len(data) {
		return fmt.Errorf("country logits batch size %d does not match input batch size %d", len(countryLogits), len(data))
	}

	tagCount := len(r.bioTagsToKeep)
	for batchIdx, batch := range emissions {
		if len(batch) == 0 {
			return fmt.Errorf("inference emissions batch %d has empty sequence", batchIdx)
		}
		for seqIdx, token := range batch {
			if len(token) != tagCount {
				return fmt.Errorf("inference emissions[%d][%d] tag count %d does not match configured tag count %d", batchIdx, seqIdx, len(token), tagCount)
			}
		}
	}
	return nil
}

func (r *Runner) decode(emissions [][][]float64, mask [][]bool) (paths [][]int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("decoding CRF emissions: %v", recovered)
		}
	}()
	return r.crf.Decode(emissions, mask), nil
}

func (r *Runner) marginalProbabilities(emissions [][][]float64, mask [][]bool) (probs [][][]float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("computing CRF marginal probabilities: %v", recovered)
		}
	}()
	return r.crf.MarginalProbabilities(emissions, mask), nil
}

func (r *Runner) pathToBIOTags(path []int) ([]core.BIOTag, error) {
	tags := make([]core.BIOTag, len(path))
	for i, id := range path {
		if id < 0 || id >= len(r.bioTagsToKeep) {
			return nil, fmt.Errorf("decoded tag id %d at position %d is outside configured tag range 0..%d", id, i, len(r.bioTagsToKeep)-1)
		}
		tags[i] = r.bioTagsToKeep[id]
	}
	return tags, nil
}

func (r *Runner) tagSeries(tag core.Tag, emissions [][]float64, marginals [][][]float64, batchIdx int) ([]float64, []float64) {
	probas := make([]float64, len(emissions))
	scores := make([]float64, len(emissions))
	for _, tagIdx := range r.bioTagIndices(tag) {
		for seq := range emissions {
			scores[seq] += emissions[seq][tagIdx]
			if seq < len(marginals) && batchIdx < len(marginals[seq]) && tagIdx < len(marginals[seq][batchIdx]) {
				probas[seq] += marginals[seq][batchIdx][tagIdx]
			}
		}
	}
	return probas, scores
}

func (r *Runner) bioTagIndices(tag core.Tag) []int {
	if tag == core.TagOther {
		for idx, bioTag := range r.bioTagsToKeep {
			if bioTag.BIO == core.BioOther || bioTag.Tag == core.TagOther {
				return []int{idx}
			}
		}
		return nil
	}

	var indices []int
	for idx, bioTag := range r.bioTagsToKeep {
		if bioTag.Tag != tag {
			continue
		}
		if bioTag.BIO == core.BioBefore || bioTag.BIO == core.BioInside {
			indices = append(indices, idx)
		}
	}
	return indices
}

func (r *Runner) countryPrediction(countryLogits [][]float64, batchIdx int) (string, float64) {
	if batchIdx >= len(countryLogits) || len(countryLogits[batchIdx]) == 0 {
		return "", 0
	}
	probs := softmax(countryLogits[batchIdx])
	bestIdx := 0
	for i := 1; i < len(probs); i++ {
		if probs[i] > probs[bestIdx] {
			bestIdx = i
		}
	}
	return r.idToCountry[bestIdx], probs[bestIdx]
}

func softmax(logits []float64) []float64 {
	if len(logits) == 0 {
		return nil
	}
	maxLogit := logits[0]
	for _, logit := range logits[1:] {
		if logit > maxLogit {
			maxLogit = logit
		}
	}
	probs := make([]float64, len(logits))
	sum := 0.0
	for i, logit := range logits {
		probs[i] = math.Exp(logit - maxLogit)
		sum += probs[i]
	}
	if sum == 0 || math.IsNaN(sum) {
		return probs
	}
	for i := range probs {
		probs[i] /= sum
	}
	return probs
}

func meanSlice(values []float64, start int, end int) float64 {
	if start < 0 {
		start = 0
	}
	if end > len(values) {
		end = len(values)
	}
	if start >= end {
		return 0
	}
	sum := 0.0
	for _, value := range values[start:end] {
		sum += value
	}
	return sum / float64(end-start)
}

func sliceRunes(raw string, start int, end int) string {
	if start < 0 {
		start = 0
	}
	runeCount := utf8.RuneCountInString(raw)
	if end > runeCount {
		end = runeCount
	}
	if start >= end {
		return ""
	}
	runes := []rune(raw)
	return string(runes[start:end])
}
