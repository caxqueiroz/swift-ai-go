# Redis Vector Cache Backend — Design

**Date:** 2026-07-09
**Status:** Approved

## Goal

Add Redis (via [`redis-vl-golang`](https://github.com/redis-developer/redis-vl-golang)) as an alternative Stage 1 semantic-cache backend to pgvector, selectable with a configuration variable. Both `iso-api` and `iso-cache-fill` support it; the cascade, pipeline, and API contract are unchanged.

## Non-goals

- No data migration tooling between pgvector and Redis. Operators pick a backend and fill it.
- No dual-write or fallback-across-backends mode.
- No change to embedding backends, gates, thresholds, or provenance policy.

## Configuration

New env var `CACHE_BACKEND`, values `pgvector` (default) and `redis`.

| `CACHE_BACKEND` | URL var | URL empty means |
|---|---|---|
| `pgvector` / unset | `DATABASE_URL` | Stage 1 disabled (current behavior, unchanged) |
| `redis` | `REDIS_URL` (new, e.g. `redis://localhost:6379/0`) | Stage 1 disabled |
| anything else | — | Startup error listing valid values |

- Redis backend requires **Redis 8+** (built-in vector search).
- Embedding configuration (`EMBEDDING_BACKEND`, etc.) is orthogonal and untouched.
- `iso-cache-fill` gains `--cache-backend` and `--redis-url` flags, defaulting from the env vars, consistent with its existing `--database-url` flag style.
- The connection is pinged at startup; a bad URL fails fast instead of degrading every request.

## Code structure

```
internal/cache/
  types.go      Store interface, Entry, Source — unchanged
  postgres.go   PostgresStore — unchanged
  redis.go      NEW: RedisStore implementing Store via redis-vl-golang
  factory.go    NEW: OpenStore(ctx, Config) (Store, func(), error)
```

```go
type Config struct {
    Backend     string // "pgvector" (default) | "redis"
    DatabaseURL string
    RedisURL    string
    Dimensions  int    // redis vector dims, default 384 (MiniLM)
}
```

`OpenStore` returns `(nil, noop, nil)` when the selected backend's URL is empty (Stage 1 disabled). `cmd/iso-api`'s `stage1Dependencies()` and the equivalent wiring in `cmd/iso-cache-fill` call `OpenStore` instead of `NewPostgresStore` directly, so backend selection lives in exactly one place.

## Redis data model

Auto-created at startup if missing (idempotent; no migration step):

- Index `address_cache`, HASH storage, key prefix `address_cache:`.
- `embedding`: vector field, HNSW, cosine metric, float32, dims from `Config.Dimensions` (default 384, overridable via existing `EMBEDDING_DIMENSIONS`).
- `normalized_address`, `source`: TAG fields.
- `raw_address`, `structured` (JSON string): stored unindexed.
- Document key = `address_cache:` + SHA-256 hex of the normalized address. Deterministic keys make writes natural upserts and exact lookup an O(1) `Fetch`.

Embeddings are stored as float32 (Redis requirement); float64→float32 precision loss is far below the 0.90 semantic-gate resolution.

## Store method mapping

| Method | Postgres | Redis |
|---|---|---|
| `LookupNormalized` | SQL by unique column; `ErrNoRows` → miss | `Fetch` by derived key; missing → `(Entry{}, false, nil)` |
| `Search` | `1 - (embedding <=> $1)` ordered ASC by distance | `VectorQuery(embedding, NumResults=limit)`; `SemanticScore = 1 − cosine distance` |
| `Upsert` | `ON CONFLICT` upsert with human_verified protection | `Load` one doc at derived key, same input validation, read-before-write protection (below) |

### human_verified protection

The Postgres upsert refuses to let a non-`human_verified` write downgrade a `human_verified` row: `source`, `structured`, and the derived columns are preserved; `raw_address`, `embedding`, `updated_at` are updated. The Redis `Upsert` replicates this with a read-before-write: fetch the existing doc; if its source is `human_verified` and the incoming source is not, keep the existing `source` and `structured`, write the rest.

**Known limitation:** unlike the SQL statement this is not atomic. A race between concurrent writers (offline cache-fill and API write-back) could overwrite a human-verified row with a pipeline result. Both writers produce valid rows and human verification happens offline/rarely, so this is accepted and documented rather than solved with WATCH/transactions. The merge decision is implemented as a pure function so it is unit-testable.

## Error handling

Unchanged by design: `internal/cascade` already treats every cache error as fall-through to Stage 2 (`FallbackCacheError`). A down Redis degrades to live inference exactly as a down Postgres does.

## Tooling

- `task redis:start` / `task redis:stop`: `docker run --rm -d --name swift-ai-redis -p 6379:6379 redis:8`, mirroring the pg0 helpers. No migrate task — the index auto-creates.

## Testing

- **Hermetic unit tests** (`internal/cache/redis_test.go`): key derivation, schema construction (dims/metric), distance→score conversion, human_verified merge function, input-validation parity with `PostgresStore`.
- **Gated integration test**: skips unless `REDIS_URL` is set (same pattern as the parity test). Round-trips Create/Upsert/LookupNormalized/Search against real Redis 8 including the human_verified protection.
- **Factory unit tests**: backend selection, empty URL → disabled store, invalid backend → error.

## Documentation

- README: `CACHE_BACKEND` + `REDIS_URL` in the env-var table; short "Redis cache backend" subsection (Redis 8+ requirement, auto-created index, `task redis:start`).
- CLAUDE.md: one sentence in the cache section noting the two backends.

## Dependencies

`go.mod` gains `github.com/redis-developer/redis-vl-golang` (v0.1.2+) and, transitively, `github.com/redis/go-redis`.
