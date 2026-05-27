package cache

import (
	"context"

	"github.com/tipmarket/swift-ai/internal/structured"
)

type Source string

const (
	SourceHumanVerified Source = "human_verified"
	SourceCRFPipeline   Source = "crf_pipeline"
	SourceLLMAssisted   Source = "llm_assisted"
	SourceSonnetSeed    Source = "sonnet_seed"
)

type Entry struct {
	RawAddress        string
	NormalizedAddress string
	Structured        structured.Address
	Source            Source
	Embedding         []float64
}

type Candidate struct {
	Entry         Entry
	SemanticScore float64
}

type Store interface {
	LookupNormalized(ctx context.Context, normalizedAddress string) (Entry, bool, error)
	Search(ctx context.Context, normalizedAddress string, embedding []float64, limit int) ([]Candidate, error)
	Upsert(ctx context.Context, entry Entry) error
}
