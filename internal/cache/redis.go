package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	redisvl "github.com/redis-developer/redis-vl-golang"
	"github.com/redis-developer/redis-vl-golang/query"
	"github.com/redis-developer/redis-vl-golang/schema"
	"github.com/redis-developer/redis-vl-golang/vectors"
)

// DefaultRedisDimensions matches the MiniLM 384-dimensional pgvector index.
const DefaultRedisDimensions = 384

func redisDocID(normalizedAddress string) string {
	sum := sha256.Sum256([]byte(normalizedAddress))
	return hex.EncodeToString(sum[:])
}

// mergeCacheWrite mirrors the human_verified protection in
// sql/queries/address_cache.sql: a non-human_verified write may not change a
// human_verified row's source or structured fields, but still refreshes
// raw_address and embedding.
func mergeCacheWrite(existing Entry, existingFound bool, incoming Entry) Entry {
	if !existingFound {
		return incoming
	}
	if existing.Source == SourceHumanVerified && incoming.Source != SourceHumanVerified {
		merged := incoming
		merged.Source = existing.Source
		merged.Structured = existing.Structured
		return merged
	}
	return incoming
}

// semanticScoreFromDistance converts a cosine distance into the similarity
// score the cascade gates on, matching 1 - (embedding <=> $1) in Postgres.
func semanticScoreFromDistance(distance float64) float64 {
	return 1 - distance
}

func parseDistance(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("parse vector distance %q: %w", v, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected vector distance type %T", value)
	}
}

func entryFromDoc(doc map[string]any) (Entry, error) {
	var entry Entry
	structuredJSON, _ := doc["structured"].(string)
	if err := json.Unmarshal([]byte(structuredJSON), &entry.Structured); err != nil {
		return Entry{}, fmt.Errorf("decode structured address: %w", err)
	}
	entry.RawAddress, _ = doc["raw_address"].(string)
	entry.NormalizedAddress, _ = doc["normalized_address"].(string)
	if source, ok := doc["source"].(string); ok {
		entry.Source = Source(source)
	}
	return entry, nil
}

const (
	redisIndexName = "address_cache"
	redisKeyPrefix = "address_cache"
)

// RedisStore is a Store backed by Redis via redis-vl-golang, mirroring the
// semantics of PostgresStore against a HASH-backed vector index.
type RedisStore struct {
	index *redisvl.SearchIndex
}

func redisIndexSchema(dimensions int) (*schema.IndexSchema, error) {
	if dimensions <= 0 {
		dimensions = DefaultRedisDimensions
	}
	vectorField, err := schema.NewVectorField("embedding", schema.VectorAttrs{
		Algorithm:      schema.HNSW,
		Dims:           dimensions,
		DistanceMetric: schema.Cosine,
		Datatype:       "float32",
	})
	if err != nil {
		return nil, fmt.Errorf("build embedding vector field: %w", err)
	}
	indexSchema, err := schema.NewIndexSchema(schema.IndexInfo{
		Name:        redisIndexName,
		Prefixes:    []string{redisKeyPrefix},
		StorageType: schema.Hash,
	},
		vectorField,
		schema.NewTagField("normalized_address"),
		schema.NewTagField("source"),
	)
	if err != nil {
		return nil, fmt.Errorf("build address cache schema: %w", err)
	}
	return indexSchema, nil
}

// NewRedisStore connects to Redis at redisURL, pings it, and idempotently
// creates the address_cache index if it does not already exist.
// dimensions <= 0 falls back to DefaultRedisDimensions.
func NewRedisStore(ctx context.Context, redisURL string, dimensions int) (*RedisStore, error) {
	if strings.TrimSpace(redisURL) == "" {
		return nil, errors.New("redis URL is required")
	}
	indexSchema, err := redisIndexSchema(dimensions)
	if err != nil {
		return nil, err
	}
	index, err := redisvl.NewSearchIndexFromURL(indexSchema, redisURL)
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	if err := index.Client().Ping(ctx).Err(); err != nil {
		_ = index.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	exists, err := index.Exists(ctx)
	if err != nil {
		_ = index.Close()
		return nil, fmt.Errorf("check address cache index: %w", err)
	}
	if !exists {
		if err := index.Create(ctx); err != nil {
			_ = index.Close()
			return nil, fmt.Errorf("create address cache index: %w", err)
		}
	}
	return &RedisStore{index: index}, nil
}

// Close releases the underlying Redis connection. It is safe to call on a
// nil store or a store with no connection.
func (s *RedisStore) Close() {
	if s != nil && s.index != nil {
		_ = s.index.Close()
	}
}

func (s *RedisStore) LookupNormalized(ctx context.Context, normalizedAddress string) (Entry, bool, error) {
	if strings.TrimSpace(normalizedAddress) == "" {
		return Entry{}, false, errors.New("normalized address is required")
	}
	if s == nil || s.index == nil {
		return Entry{}, false, errors.New("redis store is nil")
	}

	doc, err := s.index.Fetch(ctx, redisDocID(normalizedAddress))
	if err != nil {
		return Entry{}, false, fmt.Errorf("lookup normalized address: %w", err)
	}
	if doc == nil {
		return Entry{}, false, nil
	}
	entry, err := entryFromDoc(doc)
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (s *RedisStore) Search(ctx context.Context, _ string, embedding []float64, limit int) ([]Candidate, error) {
	if len(embedding) == 0 {
		return nil, errors.New("embedding is required")
	}
	if s == nil || s.index == nil {
		return nil, errors.New("redis store is nil")
	}
	if limit <= 0 {
		limit = 5
	}

	q := query.NewVectorQuery("embedding", embedding).NumResults(limit)
	rows, err := s.index.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("search address cache: %w", err)
	}

	candidates := make([]Candidate, 0, limit)
	for _, row := range rows {
		entry, err := entryFromDoc(row)
		if err != nil {
			return nil, err
		}
		distance, err := parseDistance(row[query.DistanceID])
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, Candidate{
			Entry:         entry,
			SemanticScore: semanticScoreFromDistance(distance),
		})
	}
	return candidates, nil
}

func (s *RedisStore) Upsert(ctx context.Context, entry Entry) error {
	if entry.NormalizedAddress == "" {
		return errors.New("normalized address is required")
	}
	if len(entry.Embedding) == 0 {
		return errors.New("embedding is required")
	}
	if entry.Source == "" {
		return errors.New("source is required")
	}
	if s == nil || s.index == nil {
		return errors.New("redis store is nil")
	}

	// Not atomic like the SQL ON CONFLICT clause: a concurrent writer could
	// race between this read and the write below. Accepted per the design
	// spec; both writers produce valid pipeline rows.
	existing, found, err := s.LookupNormalized(ctx, entry.NormalizedAddress)
	if err != nil {
		return fmt.Errorf("read existing cache row: %w", err)
	}
	merged := mergeCacheWrite(existing, found, entry)

	structuredJSON, err := json.Marshal(merged.Structured)
	if err != nil {
		return fmt.Errorf("encode structured address: %w", err)
	}
	embeddingBuf, err := vectors.ToBuffer(merged.Embedding, vectors.Float32)
	if err != nil {
		return fmt.Errorf("encode embedding: %w", err)
	}

	doc := map[string]any{
		"raw_address":        merged.RawAddress,
		"normalized_address": merged.NormalizedAddress,
		"source":             string(merged.Source),
		"structured":         string(structuredJSON),
		"embedding":          embeddingBuf,
	}
	if _, err := s.index.Load(ctx, []map[string]any{doc}, redisvl.LoadOptions{
		Keys: []string{s.index.Key(redisDocID(merged.NormalizedAddress))},
	}); err != nil {
		return fmt.Errorf("upsert address cache: %w", err)
	}
	return nil
}

var _ Store = (*RedisStore)(nil)
