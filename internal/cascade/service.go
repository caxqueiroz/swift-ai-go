package cascade

import (
	"context"
	"errors"
	"fmt"

	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/structured"
)

type ServedBy string

const (
	ServedByStage1Cache    ServedBy = "stage1_cache"
	ServedByStage2Pipeline ServedBy = "stage2_pipeline"
)

const (
	FallbackCacheMiss            = "cache_miss"
	FallbackStage1Unconfigured   = "stage1_unconfigured"
	FallbackEmbeddingError       = "embedding_error"
	FallbackCacheError           = "cache_error"
	FallbackSemanticGateFailed   = "semantic_gate_failed"
	FallbackLexicalGateFailed    = "lexical_gate_failed"
	FallbackUntrustedCacheSource = "untrusted_cache_source"
)

type Request struct {
	Text string
}

type Item struct {
	Input          string             `json:"input"`
	Structured     structured.Address `json:"structured"`
	ServedBy       ServedBy           `json:"served_by"`
	CacheSource    cache.Source       `json:"cache_source,omitempty"`
	SemanticScore  *float64           `json:"semantic_score,omitempty"`
	LexicalScore   *float64           `json:"lexical_score,omitempty"`
	FallbackReason string             `json:"fallback_reason,omitempty"`
}

type Pipeline interface {
	Run(ctx context.Context, samples []core.AddressSample) ([]core.Result, error)
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type Config struct {
	SemanticThreshold float64
	LexicalThreshold  float64
	SearchLimit       int
	TrustSonnetSeed   bool
	TrustLLMAssisted  bool
}

type Option func(*Service)

type Service struct {
	pipeline Pipeline
	cache    cache.Store
	embedder Embedder
	cfg      Config
}

func DefaultConfig() Config {
	return Config{
		SemanticThreshold: 0.90,
		LexicalThreshold:  0.85,
		SearchLimit:       5,
	}
}

func NewService(pipeline Pipeline, opts ...Option) *Service {
	s := &Service{
		pipeline: pipeline,
		cfg:      DefaultConfig(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithCache(store cache.Store) Option {
	return func(s *Service) {
		s.cache = store
	}
}

func WithEmbedder(embedder Embedder) Option {
	return func(s *Service) {
		s.embedder = embedder
	}
}

func WithConfig(cfg Config) Option {
	return func(s *Service) {
		if cfg.SemanticThreshold > 0 {
			s.cfg.SemanticThreshold = cfg.SemanticThreshold
		}
		if cfg.LexicalThreshold > 0 {
			s.cfg.LexicalThreshold = cfg.LexicalThreshold
		}
		if cfg.SearchLimit > 0 {
			s.cfg.SearchLimit = cfg.SearchLimit
		}
		s.cfg.TrustSonnetSeed = cfg.TrustSonnetSeed
		s.cfg.TrustLLMAssisted = cfg.TrustLLMAssisted
	}
}

func (s *Service) Convert(ctx context.Context, requests []Request) ([]Item, error) {
	if s == nil {
		return nil, errors.New("convert service is nil")
	}
	if s.pipeline == nil {
		return nil, errors.New("pipeline is required")
	}

	items := make([]Item, len(requests))
	misses := make([]miss, 0, len(requests))
	for i, request := range requests {
		normalized := NormalizeAddress(request.Text)
		if normalized == "" {
			return nil, fmt.Errorf("item %d: text is required", i)
		}

		item, miss := s.tryStage1(ctx, i, request, normalized)
		if miss == nil {
			items[i] = item
			continue
		}
		misses = append(misses, *miss)
	}

	if len(misses) == 0 {
		return items, nil
	}

	samples := make([]core.AddressSample, len(misses))
	for i, miss := range misses {
		samples[i] = core.AddressSample{Text: miss.request.Text}
	}
	results, err := s.pipeline.Run(ctx, samples)
	if err != nil {
		return nil, fmt.Errorf("run stage2 pipeline: %w", err)
	}
	if len(results) != len(misses) {
		return nil, fmt.Errorf("stage2 pipeline returned %d results, want %d", len(results), len(misses))
	}

	for i, result := range results {
		miss := misses[i]
		address := structured.FromResult(result)
		items[miss.index] = Item{
			Input:          miss.request.Text,
			Structured:     address,
			ServedBy:       ServedByStage2Pipeline,
			CacheSource:    cache.SourceCRFPipeline,
			SemanticScore:  miss.semanticScore,
			LexicalScore:   miss.lexicalScore,
			FallbackReason: miss.reason,
		}

		if s.cache != nil && len(miss.embedding) > 0 {
			_ = s.cache.Upsert(ctx, cache.Entry{
				RawAddress:        miss.request.Text,
				NormalizedAddress: miss.normalized,
				Structured:        address,
				Source:            cache.SourceCRFPipeline,
				Embedding:         miss.embedding,
			})
		}
	}

	return items, nil
}

func (s *Service) tryStage1(ctx context.Context, index int, request Request, normalized string) (Item, *miss) {
	if s.cache == nil || s.embedder == nil {
		return Item{}, &miss{index: index, request: request, normalized: normalized, reason: FallbackStage1Unconfigured}
	}

	embedding, err := s.embedder.Embed(ctx, normalized)
	if err != nil {
		return Item{}, &miss{index: index, request: request, normalized: normalized, reason: FallbackEmbeddingError}
	}
	candidates, err := s.cache.Search(ctx, normalized, embedding, s.cfg.SearchLimit)
	if err != nil {
		return Item{}, &miss{index: index, request: request, normalized: normalized, embedding: embedding, reason: FallbackCacheError}
	}
	if len(candidates) == 0 {
		return Item{}, &miss{index: index, request: request, normalized: normalized, embedding: embedding, reason: FallbackCacheMiss}
	}

	fallback := miss{index: index, request: request, normalized: normalized, embedding: embedding, reason: FallbackCacheMiss}
	for _, candidate := range candidates {
		semantic := candidate.SemanticScore
		lexical := LexicalIdentity(normalized, candidate.Entry.NormalizedAddress)
		fallback.semanticScore = &semantic
		fallback.lexicalScore = &lexical

		if semantic < s.cfg.SemanticThreshold {
			fallback.reason = FallbackSemanticGateFailed
			continue
		}
		if lexical < s.cfg.LexicalThreshold {
			fallback.reason = FallbackLexicalGateFailed
			continue
		}
		if !s.trusted(candidate.Entry.Source) {
			fallback.reason = FallbackUntrustedCacheSource
			continue
		}

		return Item{
			Input:         request.Text,
			Structured:    candidate.Entry.Structured,
			ServedBy:      ServedByStage1Cache,
			CacheSource:   candidate.Entry.Source,
			SemanticScore: &semantic,
			LexicalScore:  &lexical,
		}, nil
	}

	return Item{}, &fallback
}

func (s *Service) trusted(source cache.Source) bool {
	switch source {
	case cache.SourceHumanVerified, cache.SourceCRFPipeline:
		return true
	case cache.SourceSonnetSeed:
		return s.cfg.TrustSonnetSeed
	case cache.SourceLLMAssisted:
		return s.cfg.TrustLLMAssisted
	default:
		return false
	}
}

type miss struct {
	index         int
	request       Request
	normalized    string
	embedding     []float64
	reason        string
	semanticScore *float64
	lexicalScore  *float64
}
