package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tipmarket/swift-ai/internal/cache/cachedb"
)

type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *cachedb.Queries
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
	return &PostgresStore{
		pool:    pool,
		queries: cachedb.New(pool),
	}, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) LookupNormalized(ctx context.Context, normalizedAddress string) (Entry, bool, error) {
	if s == nil || s.queries == nil {
		return Entry{}, false, errors.New("postgres store is nil")
	}
	if strings.TrimSpace(normalizedAddress) == "" {
		return Entry{}, false, errors.New("normalized address is required")
	}

	row, err := s.queries.LookupAddressCacheByNormalized(ctx, normalizedAddress)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("lookup normalized address: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(row.Structured, &entry.Structured); err != nil {
		return Entry{}, false, fmt.Errorf("decode structured address: %w", err)
	}
	entry.RawAddress = row.RawAddress
	entry.NormalizedAddress = row.NormalizedAddress
	entry.Source = Source(row.Source)
	return entry, true, nil
}

func (s *PostgresStore) Search(ctx context.Context, _ string, embedding []float64, limit int) ([]Candidate, error) {
	if s == nil || s.queries == nil {
		return nil, errors.New("postgres store is nil")
	}
	if len(embedding) == 0 {
		return nil, errors.New("embedding is required")
	}
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.queries.SearchAddressCache(ctx, cachedb.SearchAddressCacheParams{
		Embedding:   VectorLiteral(embedding),
		ResultLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("search address cache: %w", err)
	}

	candidates := make([]Candidate, 0, limit)
	for _, row := range rows {
		var entry Entry
		if err := json.Unmarshal(row.Structured, &entry.Structured); err != nil {
			return nil, fmt.Errorf("decode structured address: %w", err)
		}
		entry.RawAddress = row.RawAddress
		entry.NormalizedAddress = row.NormalizedAddress
		entry.Source = Source(row.Source)
		candidates = append(candidates, Candidate{Entry: entry, SemanticScore: row.SemanticScore})
	}
	return candidates, nil
}

func (s *PostgresStore) Upsert(ctx context.Context, entry Entry) error {
	if s == nil || s.queries == nil {
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

	if err := s.queries.UpsertAddressCache(ctx, cachedb.UpsertAddressCacheParams{
		RawAddress:        entry.RawAddress,
		NormalizedAddress: entry.NormalizedAddress,
		Embedding:         VectorLiteral(entry.Embedding),
		Source:            string(entry.Source),
		Structured:        structuredJSON,
		CountryCode:       textValue(entry.Structured.Country),
		Town:              textValue(entry.Structured.Town),
		PostalCode:        textValue(entry.Structured.PostalCode),
		Street:            textValue(entry.Structured.Street),
	}); err != nil {
		return fmt.Errorf("upsert address cache: %w", err)
	}
	return nil
}

func textValue(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func VectorLiteral(vector []float64) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
