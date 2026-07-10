package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
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
