# Redis Vector Cache Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Redis (via redis-vl-golang) as an alternative Stage 1 semantic-cache backend to pgvector, selectable with `CACHE_BACKEND=pgvector|redis`.

**Architecture:** A new `RedisStore` in `internal/cache/redis.go` implements the existing `cache.Store` interface (`LookupNormalized`, `Search`, `Upsert`). A new `cache.OpenStore(ctx, cache.Config)` factory in `internal/cache/factory.go` centralizes backend selection; `cmd/iso-api` and `cmd/iso-cache-fill` call it instead of `cache.NewPostgresStore` directly. Cascade, pipeline, API contract, and error handling are untouched.

**Tech Stack:** Go 1.26.3, `github.com/redis-developer/redis-vl-golang` (v0.1.2+, on go-redis), Redis 8+ (built-in vector search), existing pgvector path unchanged.

**Spec:** `docs/superpowers/specs/2026-07-09-redis-cache-backend-design.md`

## Global Constraints

- Go `1.26.3`; module `github.com/tipmarket/swift-ai`.
- `CACHE_BACKEND` values are exactly `pgvector` (default when unset/empty) and `redis`; anything else is a startup error listing the valid values.
- Redis backend reads `REDIS_URL` (e.g. `redis://localhost:6379/0`); empty `REDIS_URL` with `CACHE_BACKEND=redis` means Stage 1 disabled, mirroring empty `DATABASE_URL` behavior.
- Redis index: name `address_cache`, HASH storage, key prefix `address_cache`, HNSW, cosine, float32, dims default `384`.
- `SemanticScore` must equal `1 − cosine distance` (same meaning as the SQL's `1 - (embedding <=> $1)`).
- The `human_verified` protection from `sql/queries/address_cache.sql` must hold: a non-`human_verified` write may not change a `human_verified` row's `source` or `structured`; it does update `raw_address` and `embedding`.
- Tests: plain `go test ./...` works; run `task verify` (tests + golangci-lint) before finishing.
- Code style: `log/slog` only, error wrapping `fmt.Errorf("context: %w", err)`, early returns, no `any` in interface method parameters.
- Commits: `type(scope): subject` + blank line + `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Do NOT commit unrelated files — the working tree has pre-existing modifications. `git add` only the paths named in each commit step.

---

### Task 1: Pure helpers for the Redis store

Deterministic key derivation, human_verified merge policy, distance→score conversion, query-row decoding. No new dependency yet — these are pure functions in package `cache`.

**Files:**
- Create: `internal/cache/redis.go` (helpers only in this task)
- Test: `internal/cache/redis_test.go`

**Interfaces:**
- Consumes: `Entry`, `Source`, `SourceHumanVerified` from `internal/cache/types.go` (existing).
- Produces (used by Task 2):
  - `func redisDocID(normalizedAddress string) string` — SHA-256 hex of the input
  - `func mergeCacheWrite(existing Entry, existingFound bool, incoming Entry) Entry`
  - `func semanticScoreFromDistance(distance float64) float64`
  - `func parseDistance(value any) (float64, error)`
  - `func entryFromDoc(doc map[string]any) (Entry, error)`
  - `const DefaultRedisDimensions = 384`

- [ ] **Step 1: Write the failing tests**

Create `internal/cache/redis_test.go`:

```go
package cache

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestRedisDocIDIsStableSHA256Hex(t *testing.T) {
	got := redisDocID("77 rue de rivoli 75001 paris")
	want := "6f6e2745b8d70c807e9742de7fdcf219cdf53457e48af3d92684146fdeb90727"
	if got != want {
		t.Fatalf("redisDocID() = %q, want %q", got, want)
	}
	if redisDocID("other input") == got {
		t.Fatal("redisDocID() must differ for different inputs")
	}
}

func TestMergeCacheWriteProtectsHumanVerifiedRows(t *testing.T) {
	humanRow := Entry{
		RawAddress:        "old raw",
		NormalizedAddress: "norm",
		Source:            SourceHumanVerified,
		Structured:        structured.Address{Country: "FR", Town: "PARIS"},
		Embedding:         []float64{0.1},
	}
	pipelineWrite := Entry{
		RawAddress:        "new raw",
		NormalizedAddress: "norm",
		Source:            SourceCRFPipeline,
		Structured:        structured.Address{Country: "DE", Town: "BERLIN"},
		Embedding:         []float64{0.2},
	}

	tests := []struct {
		name          string
		existing      Entry
		existingFound bool
		incoming      Entry
		wantSource    Source
		wantTown      string
		wantRaw       string
		wantEmbedding float64
	}{
		{
			name:          "no existing row keeps incoming",
			existingFound: false,
			incoming:      pipelineWrite,
			wantSource:    SourceCRFPipeline,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name:          "pipeline write cannot downgrade human_verified",
			existing:      humanRow,
			existingFound: true,
			incoming:      pipelineWrite,
			wantSource:    SourceHumanVerified,
			wantTown:      "PARIS",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name:          "human_verified write overwrites human_verified",
			existing:      humanRow,
			existingFound: true,
			incoming: Entry{
				RawAddress:        "new raw",
				NormalizedAddress: "norm",
				Source:            SourceHumanVerified,
				Structured:        structured.Address{Country: "DE", Town: "BERLIN"},
				Embedding:         []float64{0.2},
			},
			wantSource:    SourceHumanVerified,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name: "pipeline write overwrites pipeline row",
			existing: Entry{
				RawAddress:        "old raw",
				NormalizedAddress: "norm",
				Source:            SourceLLMAssisted,
				Structured:        structured.Address{Country: "FR", Town: "PARIS"},
				Embedding:         []float64{0.1},
			},
			existingFound: true,
			incoming:      pipelineWrite,
			wantSource:    SourceCRFPipeline,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeCacheWrite(tt.existing, tt.existingFound, tt.incoming)
			if merged.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", merged.Source, tt.wantSource)
			}
			if merged.Structured.Town != tt.wantTown {
				t.Fatalf("Structured.Town = %q, want %q", merged.Structured.Town, tt.wantTown)
			}
			if merged.RawAddress != tt.wantRaw {
				t.Fatalf("RawAddress = %q, want %q", merged.RawAddress, tt.wantRaw)
			}
			if len(merged.Embedding) != 1 || merged.Embedding[0] != tt.wantEmbedding {
				t.Fatalf("Embedding = %v, want [%v]", merged.Embedding, tt.wantEmbedding)
			}
		})
	}
}

func TestSemanticScoreFromDistanceMatchesPgvectorSemantics(t *testing.T) {
	if got := semanticScoreFromDistance(0.0); got != 1.0 {
		t.Fatalf("semanticScoreFromDistance(0) = %v, want 1", got)
	}
	if got := semanticScoreFromDistance(0.25); got != 0.75 {
		t.Fatalf("semanticScoreFromDistance(0.25) = %v, want 0.75", got)
	}
}

func TestParseDistanceAcceptsFloatAndString(t *testing.T) {
	if got, err := parseDistance(0.5); err != nil || got != 0.5 {
		t.Fatalf("parseDistance(0.5) = %v, %v; want 0.5, nil", got, err)
	}
	if got, err := parseDistance("0.125"); err != nil || got != 0.125 {
		t.Fatalf("parseDistance(\"0.125\") = %v, %v; want 0.125, nil", got, err)
	}
	if _, err := parseDistance(nil); err == nil {
		t.Fatal("parseDistance(nil) must return an error")
	}
	if _, err := parseDistance("not-a-number"); err == nil {
		t.Fatal("parseDistance(\"not-a-number\") must return an error")
	}
}

func TestEntryFromDocDecodesStoredFields(t *testing.T) {
	doc := map[string]any{
		"raw_address":        "77 RUE DE RIVOLI 75001 PARIS",
		"normalized_address": "77 rue de rivoli 75001 paris",
		"source":             "crf_pipeline",
		"structured":         `{"address_line":"77 RUE DE RIVOLI 75001 PARIS","country":"FR","town":"PARIS"}`,
	}
	entry, err := entryFromDoc(doc)
	if err != nil {
		t.Fatalf("entryFromDoc() error = %v", err)
	}
	if entry.RawAddress != "77 RUE DE RIVOLI 75001 PARIS" {
		t.Fatalf("RawAddress = %q", entry.RawAddress)
	}
	if entry.NormalizedAddress != "77 rue de rivoli 75001 paris" {
		t.Fatalf("NormalizedAddress = %q", entry.NormalizedAddress)
	}
	if entry.Source != SourceCRFPipeline {
		t.Fatalf("Source = %q, want crf_pipeline", entry.Source)
	}
	if entry.Structured.Country != "FR" || entry.Structured.Town != "PARIS" {
		t.Fatalf("Structured = %+v", entry.Structured)
	}
}

func TestEntryFromDocRejectsInvalidStructuredJSON(t *testing.T) {
	if _, err := entryFromDoc(map[string]any{"structured": "{not json"}); err == nil {
		t.Fatal("entryFromDoc() must reject invalid structured JSON")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cache -run 'TestRedisDocID|TestMergeCacheWrite|TestSemanticScore|TestParseDistance|TestEntryFromDoc' -v`
Expected: compile FAIL — `undefined: redisDocID`, `undefined: mergeCacheWrite`, etc.

- [ ] **Step 3: Write the helpers**

Create `internal/cache/redis.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cache -run 'TestRedisDocID|TestMergeCacheWrite|TestSemanticScore|TestParseDistance|TestEntryFromDoc' -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cache/redis.go internal/cache/redis_test.go
git commit -m "feat(cache): add redis store helper functions

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: RedisStore implementing cache.Store

Adds the redis-vl-golang dependency and the `RedisStore` type: schema, constructor with startup ping + idempotent index creation, `LookupNormalized`/`Search`/`Upsert`, `Close`.

**Files:**
- Modify: `internal/cache/redis.go` (append to Task 1's helpers)
- Modify: `go.mod`, `go.sum` (via `go get`)
- Test: `internal/cache/redis_test.go` (append)

**Interfaces:**
- Consumes: Task 1 helpers; `Store`, `Entry`, `Candidate` from `types.go`; redis-vl-golang packages.
- Produces (used by Tasks 3 and 6):
  - `func NewRedisStore(ctx context.Context, redisURL string, dimensions int) (*RedisStore, error)` — pings Redis, creates index `address_cache` if missing; `dimensions <= 0` falls back to `DefaultRedisDimensions`.
  - `func (s *RedisStore) Close()`
  - `*RedisStore` satisfies `Store`.

- [ ] **Step 1: Add the dependency and verify the API surface**

```bash
go get github.com/redis-developer/redis-vl-golang@latest
go doc github.com/redis-developer/redis-vl-golang/schema | head -60
go doc github.com/redis-developer/redis-vl-golang | head -60
go doc github.com/redis-developer/redis-vl-golang/query VectorQuery | head -40
go doc github.com/redis-developer/redis-vl-golang/vectors | head -30
```

The implementation below is written against the documented v0.1.x API (`schema.NewIndexSchema`, `schema.NewVectorField`, `schema.NewTagField`, `redisvl.NewSearchIndexFromURL`, `index.Exists/Create/Fetch/Query/Load/Key/Client/Close`, `query.NewVectorQuery(...).NumResults(n)`, `vectors.ToBuffer(v, vectors.Float32)`, processed query rows carrying an `embedding_distance` field). If `go doc` shows different identifier spellings (e.g. enum names for `HNSW`/`Cosine`/`Hash`, or `LoadOptions` field names), adjust the code to the real names — the behavior contract in this task is what must not change.

- [ ] **Step 2: Write the failing tests**

Append to `internal/cache/redis_test.go`:

```go
func TestNewRedisStoreRequiresURL(t *testing.T) {
	if _, err := NewRedisStore(context.Background(), "  ", 0); err == nil {
		t.Fatal("NewRedisStore() must reject an empty redis URL")
	}
}

func TestRedisStoreMethodsRejectNilStore(t *testing.T) {
	var s *RedisStore
	ctx := context.Background()

	if _, _, err := s.LookupNormalized(ctx, "x"); err == nil {
		t.Fatal("LookupNormalized on nil store must error")
	}
	if _, err := s.Search(ctx, "x", []float64{0.1}, 5); err == nil {
		t.Fatal("Search on nil store must error")
	}
	if err := s.Upsert(ctx, Entry{}); err == nil {
		t.Fatal("Upsert on nil store must error")
	}
	s.Close() // must not panic
}

func TestRedisStoreValidatesInputsBeforeNetwork(t *testing.T) {
	// A store with a nil index still validates inputs first, proving the
	// same validation contract as PostgresStore without needing a server.
	s := &RedisStore{}
	ctx := context.Background()

	if _, _, err := s.LookupNormalized(ctx, "   "); err == nil {
		t.Fatal("LookupNormalized must require a normalized address")
	}
	if _, err := s.Search(ctx, "x", nil, 5); err == nil {
		t.Fatal("Search must require an embedding")
	}
	if err := s.Upsert(ctx, Entry{Embedding: []float64{0.1}, Source: SourceCRFPipeline}); err == nil {
		t.Fatal("Upsert must require a normalized address")
	}
	if err := s.Upsert(ctx, Entry{NormalizedAddress: "n", Source: SourceCRFPipeline}); err == nil {
		t.Fatal("Upsert must require an embedding")
	}
	if err := s.Upsert(ctx, Entry{NormalizedAddress: "n", Embedding: []float64{0.1}}); err == nil {
		t.Fatal("Upsert must require a source")
	}
}

func TestRedisIndexSchemaBuildsWithDefaultAndCustomDims(t *testing.T) {
	if _, err := redisIndexSchema(0); err != nil {
		t.Fatalf("redisIndexSchema(0) error = %v", err)
	}
	if _, err := redisIndexSchema(1536); err != nil {
		t.Fatalf("redisIndexSchema(1536) error = %v", err)
	}
}
```

Add `"context"` to the test file's imports.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/cache -run 'TestNewRedisStore|TestRedisStoreMethods|TestRedisStoreValidates|TestRedisIndexSchema' -v`
Expected: compile FAIL — `undefined: NewRedisStore`, `undefined: RedisStore`, `undefined: redisIndexSchema`

- [ ] **Step 4: Implement RedisStore**

Append to `internal/cache/redis.go` (extend the import block with `"context"`, `"errors"`, `"strings"`, and the redis-vl packages):

```go
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

const (
	redisIndexName = "address_cache"
	redisKeyPrefix = "address_cache"
)

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
		distance, err := parseDistance(row["embedding_distance"])
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
```

Note the validation-before-nil-check ordering in each method: input validation runs first so `TestRedisStoreValidatesInputsBeforeNetwork` passes on a zero-value store. `Upsert` validates, then read-merges via `LookupNormalized` (which needs the index), so the nil check sits between validation and the read.

- [ ] **Step 5: Add a compile-time interface assertion**

Append to `internal/cache/redis.go`:

```go
var _ Store = (*RedisStore)(nil)
```

- [ ] **Step 6: Run the full cache package tests**

Run: `go test ./internal/cache -v`
Expected: PASS (all Task 1 + Task 2 tests plus existing postgres tests)

- [ ] **Step 7: Tidy and lint**

```bash
go mod tidy
golangci-lint run ./internal/cache/...
```
Expected: no findings

- [ ] **Step 8: Commit**

```bash
git add internal/cache/redis.go internal/cache/redis_test.go go.mod go.sum
git commit -m "feat(cache): add RedisStore backed by redis-vl-golang

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: OpenStore factory

Single place that maps `Config` → concrete store. Returns a nil store (Stage 1 disabled) when the selected backend's URL is empty, preserving today's behavior.

**Files:**
- Create: `internal/cache/factory.go`
- Test: `internal/cache/factory_test.go`

**Interfaces:**
- Consumes: `NewPostgresStore` (existing), `NewRedisStore` (Task 2).
- Produces (used by Tasks 4 and 5):
  - `const BackendPgvector = "pgvector"`, `const BackendRedis = "redis"`
  - `type Config struct { Backend, DatabaseURL, RedisURL string; Dimensions int }`
  - `func OpenStore(ctx context.Context, cfg Config) (Store, func(), error)` — `(nil, noop, nil)` when disabled; the returned func is always safe to call.

- [ ] **Step 1: Write the failing tests**

Create `internal/cache/factory_test.go`:

```go
package cache

import (
	"context"
	"strings"
	"testing"
)

func TestOpenStoreDisablesStage1WhenURLEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "default backend without database URL", cfg: Config{}},
		{name: "explicit pgvector without database URL", cfg: Config{Backend: "pgvector"}},
		{name: "redis without redis URL", cfg: Config{Backend: "redis"}},
		{name: "redis ignores database URL", cfg: Config{Backend: "redis", DatabaseURL: "postgres://ignored"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, closeStore, err := OpenStore(context.Background(), tt.cfg)
			if err != nil {
				t.Fatalf("OpenStore() error = %v", err)
			}
			if store != nil {
				t.Fatal("OpenStore() must return a nil store when the backend URL is empty")
			}
			closeStore() // must not panic
		})
	}
}

func TestOpenStoreRejectsUnknownBackend(t *testing.T) {
	_, closeStore, err := OpenStore(context.Background(), Config{Backend: "memcached"})
	if err == nil {
		t.Fatal("OpenStore() must reject an unknown backend")
	}
	if !strings.Contains(err.Error(), "pgvector") || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("error must list valid backends, got %q", err.Error())
	}
	if closeStore == nil {
		t.Fatal("closeStore must be non-nil even on error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cache -run TestOpenStore -v`
Expected: compile FAIL — `undefined: OpenStore`, `undefined: Config`

- [ ] **Step 3: Implement the factory**

Create `internal/cache/factory.go`:

```go
package cache

import (
	"context"
	"fmt"
	"strings"
)

const (
	BackendPgvector = "pgvector"
	BackendRedis    = "redis"
)

// Config selects and configures the Stage 1 semantic cache backend.
type Config struct {
	Backend     string // BackendPgvector (default) or BackendRedis
	DatabaseURL string // pgvector backend
	RedisURL    string // redis backend
	Dimensions  int    // redis vector dimensions; <= 0 means DefaultRedisDimensions
}

// OpenStore opens the configured cache backend. It returns a nil Store with a
// no-op closer when the selected backend's URL is empty, which disables
// Stage 1 and keeps the caller serving through the live pipeline.
func OpenStore(ctx context.Context, cfg Config) (Store, func(), error) {
	noop := func() {}
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		backend = BackendPgvector
	}
	switch backend {
	case BackendPgvector:
		if strings.TrimSpace(cfg.DatabaseURL) == "" {
			return nil, noop, nil
		}
		store, err := NewPostgresStore(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, noop, err
		}
		return store, store.Close, nil
	case BackendRedis:
		if strings.TrimSpace(cfg.RedisURL) == "" {
			return nil, noop, nil
		}
		store, err := NewRedisStore(ctx, cfg.RedisURL, cfg.Dimensions)
		if err != nil {
			return nil, noop, err
		}
		return store, store.Close, nil
	default:
		return nil, noop, fmt.Errorf("unsupported cache backend %q (valid: %s, %s)", cfg.Backend, BackendPgvector, BackendRedis)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cache -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cache/factory.go internal/cache/factory_test.go
git commit -m "feat(cache): add OpenStore backend selection factory

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Wire iso-api

`CACHE_BACKEND`/`REDIS_URL` env vars, `--cache-backend`/`--redis-url` flags, and `stage1Dependencies` switching to `OpenStore`.

**Files:**
- Modify: `cmd/iso-api/main.go` (options struct ~line 27, `parseArgs` ~line 144, `stage1Dependencies` ~line 220)
- Test: `cmd/iso-api/main_test.go`

**Interfaces:**
- Consumes: `cache.OpenStore`, `cache.Config`, `cache.BackendPgvector` (Task 3).
- Produces: `options.cacheBackend`, `options.redisURL` fields; no exported surface.

- [ ] **Step 1: Write the failing test**

Append to `cmd/iso-api/main_test.go`:

```go
func TestParseArgsReadsCacheBackendFlags(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseArgs(nil, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.cacheBackend != "pgvector" {
		t.Fatalf("default cacheBackend = %q, want pgvector", opts.cacheBackend)
	}

	opts, err = parseArgs([]string{"--cache-backend", "redis", "--redis-url", "redis://localhost:6379/0"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.cacheBackend != "redis" {
		t.Fatalf("cacheBackend = %q, want redis", opts.cacheBackend)
	}
	if opts.redisURL != "redis://localhost:6379/0" {
		t.Fatalf("redisURL = %q", opts.redisURL)
	}
}
```

Note: `parseArgs` reads `CACHE_BACKEND`/`REDIS_URL` from the environment as defaults; the test relies on them being unset in the test environment, same as the existing `DATABASE_URL` handling.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/iso-api -run TestParseArgsReadsCacheBackendFlags -v`
Expected: compile FAIL — `opts.cacheBackend undefined`

- [ ] **Step 3: Add options fields, env defaults, and flags**

In `cmd/iso-api/main.go`, add to the `options` struct after `databaseURL string`:

```go
	cacheBackend              string
	redisURL                  string
```

In `parseArgs`, in the `opts := options{...}` literal after `databaseURL: os.Getenv("DATABASE_URL"),`:

```go
		cacheBackend:              envDefault("CACHE_BACKEND", cache.BackendPgvector),
		redisURL:                  os.Getenv("REDIS_URL"),
```

After the `--database-url` flag registration:

```go
	fs.StringVar(&opts.cacheBackend, "cache-backend", opts.cacheBackend, "semantic cache backend: pgvector or redis")
	fs.StringVar(&opts.redisURL, "redis-url", opts.redisURL, "Redis connection URL for the redis cache backend")
```

- [ ] **Step 4: Switch stage1Dependencies to OpenStore**

Replace the top of `stage1Dependencies` (currently lines 220–227):

```go
func stage1Dependencies(ctx context.Context, opts options) (cache.Store, cascade.Embedder, func(), error) {
	if opts.databaseURL == "" {
		return nil, nil, func() {}, nil
	}
	store, err := cache.NewPostgresStore(ctx, opts.databaseURL)
	if err != nil {
		return nil, nil, func() {}, err
	}
```

with:

```go
func stage1Dependencies(ctx context.Context, opts options) (cache.Store, cascade.Embedder, func(), error) {
	store, closeStore, err := cache.OpenStore(ctx, cache.Config{
		Backend:     opts.cacheBackend,
		DatabaseURL: opts.databaseURL,
		RedisURL:    opts.redisURL,
		Dimensions:  opts.embeddingDimensions,
	})
	if err != nil {
		return nil, nil, func() {}, err
	}
	if store == nil {
		return nil, nil, func() {}, nil
	}
```

Then, in the rest of `stage1Dependencies`, replace every `store.Close()` call with `closeStore()`, and replace the two success returns:
- `return store, embedder, store.Close, nil` → `return store, embedder, closeStore, nil`
- in the `onnx, local` case, `closeAll` becomes:

```go
			closeAll := func() {
				_ = embedder.Close()
				closeStore()
			}
```

- [ ] **Step 5: Run tests and build**

```bash
go test ./cmd/iso-api -v
go build ./cmd/iso-api
```
Expected: PASS, clean build

- [ ] **Step 6: Commit**

```bash
git add cmd/iso-api/main.go cmd/iso-api/main_test.go
git commit -m "feat(api): select semantic cache backend via CACHE_BACKEND

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Wire iso-cache-fill

Same flags/env vars, backend-aware URL validation, and `fillDependencies` switching to `OpenStore`.

**Files:**
- Modify: `cmd/iso-cache-fill/main.go` (fillOptions struct ~line 38, `parseArgs` env defaults ~line 169 / flags ~line 209 / validation ~line 239, `fillDependencies` ~line 607)
- Test: `cmd/iso-cache-fill/main_test.go`

**Interfaces:**
- Consumes: `cache.OpenStore`, `cache.Config`, `cache.BackendPgvector`, `cache.BackendRedis` (Task 3).
- Produces: `fillOptions.cacheBackend`, `fillOptions.redisURL`; no exported surface.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/iso-cache-fill/main_test.go` (match the file's existing `parseArgs` test style; it takes `(args, stderr)` like iso-api — check the existing `TestParseArgsReadsHoldoutFlags` at line 206 for the exact call shape and mirror it):

```go
func TestParseArgsRequiresBackendURLUnlessDryRun(t *testing.T) {
	var stderr bytes.Buffer

	// redis backend without redis URL must fail without --dry-run
	_, err := parseArgs([]string{
		"--input-path", "in.csv",
		"--cache-backend", "redis",
	}, &stderr)
	if err == nil {
		t.Fatal("parseArgs() must require --redis-url for the redis backend")
	}

	// redis backend with redis URL passes URL validation
	opts, err := parseArgs([]string{
		"--input-path", "in.csv",
		"--cache-backend", "redis",
		"--redis-url", "redis://localhost:6379/0",
		"--embedding-backend", "onnx",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.cacheBackend != "redis" || opts.redisURL != "redis://localhost:6379/0" {
		t.Fatalf("cacheBackend=%q redisURL=%q", opts.cacheBackend, opts.redisURL)
	}

	// unknown backend fails fast
	if _, err := parseArgs([]string{
		"--input-path", "in.csv",
		"--cache-backend", "memcached",
	}, &stderr); err == nil {
		t.Fatal("parseArgs() must reject an unknown cache backend")
	}

	// dry-run still skips URL requirements entirely
	if _, err := parseArgs([]string{
		"--input-path", "in.csv",
		"--cache-backend", "redis",
		"--dry-run",
	}, &stderr); err != nil {
		t.Fatalf("parseArgs() with --dry-run error = %v", err)
	}
}
```

If the existing `parseArgs` in this file has a different signature than `(args, stderr)`, adapt the calls to match the existing tests — do not change `parseArgs`'s signature.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/iso-cache-fill -run TestParseArgsRequiresBackendURL -v`
Expected: compile FAIL — unknown flag / undefined field

- [ ] **Step 3: Add fields, env defaults, flags, and validation**

In `fillOptions` after `databaseURL string`:

```go
	cacheBackend              string
	redisURL                  string
```

In `parseArgs` defaults after `databaseURL: os.Getenv("DATABASE_URL"),`:

```go
		cacheBackend:              envDefault("CACHE_BACKEND", cache.BackendPgvector),
		redisURL:                  os.Getenv("REDIS_URL"),
```

(If `envDefault` doesn't exist in this file, add the same helper used in `cmd/iso-api/main.go`:)

```go
func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
```

After the `--database-url` flag registration (~line 209):

```go
	fs.StringVar(&opts.cacheBackend, "cache-backend", opts.cacheBackend, "semantic cache backend: pgvector or redis")
	fs.StringVar(&opts.redisURL, "redis-url", opts.redisURL, "Redis connection URL for the redis cache backend")
```

Replace the URL validation inside `if !opts.dryRun {` (currently lines 239–242):

```go
	if !opts.dryRun {
		switch opts.cacheBackend {
		case cache.BackendPgvector, "":
			if opts.databaseURL == "" {
				return fillOptions{}, errors.New("database URL is required unless --dry-run is set")
			}
		case cache.BackendRedis:
			if opts.redisURL == "" {
				return fillOptions{}, errors.New("redis URL is required unless --dry-run is set")
			}
		default:
			return fillOptions{}, fmt.Errorf("unsupported cache backend %q (valid: %s, %s)", opts.cacheBackend, cache.BackendPgvector, cache.BackendRedis)
		}
		if (opts.embeddingBackend == "openai" || opts.embeddingBackend == "http") && opts.embeddingModel == "" {
			return fillOptions{}, errors.New("embedding model is required unless --dry-run is set")
		}
	}
```

Note: the whole switch (including the unknown-backend case) sits inside `!opts.dryRun`, so `--dry-run` skips backend validation entirely — dry-run never touches the cache, and this matches the test's dry-run case.

- [ ] **Step 4: Switch fillDependencies to OpenStore**

Replace the top of `fillDependencies` (currently lines 607–611):

```go
func fillDependencies(ctx context.Context, opts fillOptions) (cacheWriter, cacheEmbedder, func(), error) {
	store, err := cache.NewPostgresStore(ctx, opts.databaseURL)
	if err != nil {
		return nil, nil, func() {}, err
	}
```

with:

```go
func fillDependencies(ctx context.Context, opts fillOptions) (cacheWriter, cacheEmbedder, func(), error) {
	store, closeStore, err := cache.OpenStore(ctx, cache.Config{
		Backend:     opts.cacheBackend,
		DatabaseURL: opts.databaseURL,
		RedisURL:    opts.redisURL,
		Dimensions:  opts.embeddingDimensions,
	})
	if err != nil {
		return nil, nil, func() {}, err
	}
	if store == nil {
		return nil, nil, func() {}, errors.New("cache store is not configured")
	}
```

Then replace every `store.Close()` in the function body with `closeStore()`, and the success returns `return store, embedder, store.Close, nil` → `return store, embedder, closeStore, nil`; in the `onnx, local` case:

```go
		closeAll := func() {
			_ = embedder.Close()
			closeStore()
		}
```

- [ ] **Step 5: Run tests and build**

```bash
go test ./cmd/iso-cache-fill -v
go build ./cmd/iso-cache-fill
```
Expected: PASS, clean build

- [ ] **Step 6: Commit**

```bash
git add cmd/iso-cache-fill/main.go cmd/iso-cache-fill/main_test.go
git commit -m "feat(cache-fill): select semantic cache backend via CACHE_BACKEND

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Gated Redis integration test

Round-trips the real store against Redis 8. Skips unless `REDIS_URL` is set (same pattern as the parity test). Cleans up only its own keys so a shared dev Redis is safe.

**Files:**
- Create: `internal/cache/redis_integration_test.go`

**Interfaces:**
- Consumes: `NewRedisStore`, `redisDocID` (package-internal access), `Entry`, `SourceCRFPipeline`, `SourceHumanVerified`.
- Produces: nothing.

- [ ] **Step 1: Write the integration test**

Create `internal/cache/redis_integration_test.go`:

```go
package cache

import (
	"context"
	"os"
	"testing"

	"github.com/tipmarket/swift-ai/internal/structured"
)

// testVector returns a DefaultRedisDimensions-length unit-ish vector with a
// single dominant axis so distinct axes are far apart in cosine distance.
func testVector(axis int) []float64 {
	v := make([]float64, DefaultRedisDimensions)
	for i := range v {
		v[i] = 0.001
	}
	v[axis] = 1.0
	return v
}

func TestRedisStoreRoundTrip(t *testing.T) {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		t.Skip("REDIS_URL not set; skipping redis integration test")
	}
	ctx := context.Background()

	store, err := NewRedisStore(ctx, redisURL, DefaultRedisDimensions)
	if err != nil {
		t.Fatalf("NewRedisStore() error = %v", err)
	}

	const (
		normParis  = "itest 77 rue de rivoli 75001 paris"
		normBerlin = "itest unter den linden 1 berlin"
	)
	t.Cleanup(func() {
		client := store.index.Client()
		_ = client.Del(context.Background(),
			store.index.Key(redisDocID(normParis)),
			store.index.Key(redisDocID(normBerlin)),
		).Err()
		store.Close()
	})

	paris := Entry{
		RawAddress:        "77 RUE DE RIVOLI 75001 PARIS",
		NormalizedAddress: normParis,
		Source:            SourceCRFPipeline,
		Structured:        structured.Address{Country: "FR", Town: "PARIS", PostalCode: "75001"},
		Embedding:         testVector(0),
	}
	berlin := Entry{
		RawAddress:        "UNTER DEN LINDEN 1 BERLIN",
		NormalizedAddress: normBerlin,
		Source:            SourceCRFPipeline,
		Structured:        structured.Address{Country: "DE", Town: "BERLIN"},
		Embedding:         testVector(100),
	}
	if err := store.Upsert(ctx, paris); err != nil {
		t.Fatalf("Upsert(paris) error = %v", err)
	}
	if err := store.Upsert(ctx, berlin); err != nil {
		t.Fatalf("Upsert(berlin) error = %v", err)
	}

	// Exact lookup
	entry, found, err := store.LookupNormalized(ctx, normParis)
	if err != nil || !found {
		t.Fatalf("LookupNormalized() = found=%v, err=%v", found, err)
	}
	if entry.Structured.Town != "PARIS" || entry.Source != SourceCRFPipeline {
		t.Fatalf("lookup entry = %+v", entry)
	}

	// Miss
	if _, found, err := store.LookupNormalized(ctx, "itest no such address"); err != nil || found {
		t.Fatalf("miss lookup = found=%v, err=%v", found, err)
	}

	// Semantic search: paris vector must return paris first with score ~1
	candidates, err := store.Search(ctx, normParis, testVector(0), 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("Search() returned no candidates")
	}
	if candidates[0].Entry.NormalizedAddress != normParis {
		t.Fatalf("top candidate = %q, want %q", candidates[0].Entry.NormalizedAddress, normParis)
	}
	if candidates[0].SemanticScore < 0.99 {
		t.Fatalf("top SemanticScore = %v, want >= 0.99", candidates[0].SemanticScore)
	}

	// human_verified protection
	human := paris
	human.Source = SourceHumanVerified
	human.Structured.Town = "PARIS 1ER"
	if err := store.Upsert(ctx, human); err != nil {
		t.Fatalf("Upsert(human) error = %v", err)
	}
	downgrade := paris
	downgrade.Structured.Town = "WRONGTOWN"
	if err := store.Upsert(ctx, downgrade); err != nil {
		t.Fatalf("Upsert(downgrade) error = %v", err)
	}
	entry, found, err = store.LookupNormalized(ctx, normParis)
	if err != nil || !found {
		t.Fatalf("post-downgrade lookup = found=%v, err=%v", found, err)
	}
	if entry.Source != SourceHumanVerified {
		t.Fatalf("Source = %q, want human_verified preserved", entry.Source)
	}
	if entry.Structured.Town != "PARIS 1ER" {
		t.Fatalf("Town = %q, want PARIS 1ER preserved", entry.Structured.Town)
	}
}
```

- [ ] **Step 2: Verify it skips without REDIS_URL**

Run: `go test ./internal/cache -run TestRedisStoreRoundTrip -v`
Expected: `--- SKIP ... REDIS_URL not set`

- [ ] **Step 3: Run it against a real Redis 8**

```bash
docker run --rm -d --name swift-ai-redis-itest -p 6380:6379 redis:8
REDIS_URL=redis://localhost:6380/0 go test ./internal/cache -run TestRedisStoreRoundTrip -count=1 -v
docker stop swift-ai-redis-itest
```
Expected: PASS. If it fails on library identifier mismatches missed in Task 2, fix `internal/cache/redis.go` here — this test is the ground truth for the mapping.

- [ ] **Step 4: Commit**

```bash
git add internal/cache/redis_integration_test.go
git commit -m "test(cache): add gated redis store integration test

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Taskfile helpers and documentation

**Files:**
- Modify: `Taskfile.yml` (add `redis:start` / `redis:stop` after the `pg0:*` tasks)
- Modify: `README.md` (env-var table; new subsection after "Local pg0 Database")
- Modify: `CLAUDE.md` (cache section)

**Interfaces:** none (docs/tooling only).

- [ ] **Step 1: Add Taskfile tasks**

In `Taskfile.yml`, after the `pg0:stop` task, add:

```yaml
  redis:start:
    desc: Start the local Redis 8 cache database (Docker)
    cmds:
      - docker run --rm -d --name swift-ai-redis -p 6379:6379 redis:8

  redis:stop:
    desc: Stop the local Redis 8 cache database
    cmds:
      - docker stop swift-ai-redis
```

Verify: `task --list` shows both tasks.

- [ ] **Step 2: Update README**

In the Environment Variables table, after the `DATABASE_URL` row, add:

```markdown
| `CACHE_BACKEND` | No | Semantic cache backend: `pgvector` (default) or `redis`. |
| `REDIS_URL` | Redis Stage 1 only | Redis connection URL, e.g. `redis://localhost:6379/0`. Requires Redis 8+. |
```

After the "Local pg0 Database" section, add:

```markdown
### Redis Cache Backend

Set `CACHE_BACKEND=redis` and `REDIS_URL` to use Redis 8+ (built-in vector
search) instead of Postgres/pgvector for the Stage 1 semantic cache:

​```bash
task redis:start

CACHE_BACKEND=redis \
REDIS_URL=redis://localhost:6379/0 \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api --model-dir resources/models
​```

No migration step is needed: the store creates the `address_cache` vector
index (HNSW, cosine, 384 dimensions by default) at startup if it does not
exist. `iso-cache-fill` accepts the same configuration via `--cache-backend`
and `--redis-url`. If `REDIS_URL` is empty, Stage 1 is disabled exactly as
with an empty `DATABASE_URL`.
```

(Remove the zero-width characters before the backticks when writing — they are only here to nest the code fence in this plan.)

Also update the sentence in the intro section "Optional for Stage 1: Postgres with `pgvector`" to "Optional for Stage 1: Postgres with `pgvector`, or Redis 8+".

- [ ] **Step 3: Update CLAUDE.md**

In the "Cache & database" subsection of CLAUDE.md, add one line:

```markdown
- Two backends behind `cache.Store`, selected by `CACHE_BACKEND=pgvector|redis` (`cache.OpenStore` factory): Postgres/pgvector (`DATABASE_URL`) or Redis 8+ via redis-vl-golang (`REDIS_URL`, index auto-created at startup, `task redis:start` for local dev)
```

- [ ] **Step 4: Full verification**

```bash
task verify
```
Expected: all tests pass, no lint findings.

- [ ] **Step 5: Commit**

```bash
git add Taskfile.yml README.md CLAUDE.md
git commit -m "docs(cache): document redis cache backend and local helpers

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```
