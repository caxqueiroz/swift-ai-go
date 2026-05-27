package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) LookupNormalized(ctx context.Context, normalizedAddress string) (Entry, bool, error) {
	if s == nil || s.pool == nil {
		return Entry{}, false, errors.New("postgres store is nil")
	}
	if strings.TrimSpace(normalizedAddress) == "" {
		return Entry{}, false, errors.New("normalized address is required")
	}

	var entry Entry
	var source string
	var structuredJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT raw_address, normalized_address, structured, source
FROM address_cache
WHERE normalized_address = $1
`, normalizedAddress).Scan(&entry.RawAddress, &entry.NormalizedAddress, &structuredJSON, &source)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("lookup normalized address: %w", err)
	}
	if err := json.Unmarshal(structuredJSON, &entry.Structured); err != nil {
		return Entry{}, false, fmt.Errorf("decode structured address: %w", err)
	}
	entry.Source = Source(source)
	return entry, true, nil
}

func (s *PostgresStore) Search(ctx context.Context, _ string, embedding []float64, limit int) ([]Candidate, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("postgres store is nil")
	}
	if len(embedding) == 0 {
		return nil, errors.New("embedding is required")
	}
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.pool.Query(ctx, `
SELECT
	raw_address,
	normalized_address,
	structured,
	source,
	1 - (embedding <=> $1::vector) AS semantic_score
FROM address_cache
ORDER BY embedding <=> $1::vector
LIMIT $2
`, VectorLiteral(embedding), limit)
	if err != nil {
		return nil, fmt.Errorf("search address cache: %w", err)
	}
	defer rows.Close()

	candidates := make([]Candidate, 0, limit)
	for rows.Next() {
		var entry Entry
		var source string
		var structuredJSON []byte
		var score float64
		if err := rows.Scan(&entry.RawAddress, &entry.NormalizedAddress, &structuredJSON, &source, &score); err != nil {
			return nil, fmt.Errorf("scan address cache row: %w", err)
		}
		if err := json.Unmarshal(structuredJSON, &entry.Structured); err != nil {
			return nil, fmt.Errorf("decode structured address: %w", err)
		}
		entry.Source = Source(source)
		candidates = append(candidates, Candidate{Entry: entry, SemanticScore: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate address cache rows: %w", err)
	}
	return candidates, nil
}

func (s *PostgresStore) Upsert(ctx context.Context, entry Entry) error {
	if s == nil || s.pool == nil {
		return errors.New("postgres store is nil")
	}
	if entry.NormalizedAddress == "" {
		return errors.New("normalized address is required")
	}
	if len(entry.Embedding) == 0 {
		return errors.New("embedding is required")
	}
	if entry.Source == "" {
		return errors.New("source is required")
	}

	structuredJSON, err := json.Marshal(entry.Structured)
	if err != nil {
		return fmt.Errorf("encode structured address: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
INSERT INTO address_cache (
	raw_address,
	normalized_address,
	embedding,
	source,
	structured,
	country_code,
	town,
	postal_code,
	street
) VALUES ($1, $2, $3::vector, $4, $5::jsonb, $6, $7, $8, $9)
ON CONFLICT (normalized_address) DO UPDATE SET
	raw_address = EXCLUDED.raw_address,
	embedding = EXCLUDED.embedding,
	source = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.source
		ELSE EXCLUDED.source
	END,
	structured = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.structured
		ELSE EXCLUDED.structured
	END,
	country_code = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.country_code
		ELSE EXCLUDED.country_code
	END,
	town = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.town
		ELSE EXCLUDED.town
	END,
	postal_code = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.postal_code
		ELSE EXCLUDED.postal_code
	END,
	street = CASE
		WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.street
		ELSE EXCLUDED.street
	END,
	updated_at = now()
`, entry.RawAddress, entry.NormalizedAddress, VectorLiteral(entry.Embedding), string(entry.Source), structuredJSON,
		entry.Structured.Country, entry.Structured.Town, entry.Structured.PostalCode, entry.Structured.Street)
	if err != nil {
		return fmt.Errorf("upsert address cache: %w", err)
	}
	return nil
}

func VectorLiteral(vector []float64) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
