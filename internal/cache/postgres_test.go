package cache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/cache"
)

func TestVectorLiteralFormatsPgvectorInput(t *testing.T) {
	got := cache.VectorLiteral([]float64{0.125, -2, 3.5})
	want := "[0.125,-2,3.5]"
	if got != want {
		t.Fatalf("VectorLiteral = %q, want %q", got, want)
	}
}

func TestAddressCacheSQLCOwnsCacheQueries(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "sql", "queries", "address_cache.sql"))
	if err != nil {
		t.Fatalf("read address cache sqlc queries: %v", err)
	}
	sql := string(data)
	for _, want := range []string{
		"-- name: LookupAddressCacheByNormalized :one",
		"-- name: SearchAddressCache :many",
		"-- name: UpsertAddressCache :exec",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("address_cache.sql missing %q", want)
		}
	}
}

func TestAddressCacheMigrationCreatesMiniLMVectorIndex(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "sql", "migrations", "000001_create_address_cache.sql"))
	if err != nil {
		t.Fatalf("read address cache migration: %v", err)
	}
	sql := string(data)
	if !strings.Contains(sql, "CREATE INDEX IF NOT EXISTS address_cache_embedding_cos_384_idx") {
		t.Fatal("address cache migration must create the MiniLM vector index")
	}
	if !strings.Contains(sql, "embedding::vector(384)") {
		t.Fatal("address cache migration must pin the MiniLM vector index to 384 dimensions")
	}
}
