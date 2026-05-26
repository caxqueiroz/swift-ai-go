# Convert API Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the text-only `/convert` API, gated semantic cache, Stage 2 fallback/write-back, and offline cache fill command.

**Architecture:** `internal/cascade` owns the serving decision. `internal/cache` owns pgvector storage. `internal/structured` turns pipeline results into stable API fields. `internal/api` exposes JSON over `net/http`.

**Tech Stack:** Go standard library HTTP/JSON, existing CRF+GeoNames pipeline, pgx for Postgres/pgvector, OpenAI-compatible embeddings over `net/http`.

---

### Task 1: Structured Output

**Files:**
- Create: `internal/structured/structured.go`
- Test: `internal/structured/structured_test.go`

- [ ] Write failing tests for extracting `country`, `town`, `postal_code`, and `street` from `core.Result`.
- [ ] Implement `Address` and `FromResult`.
- [ ] Run `go test ./internal/structured`.

### Task 2: Cascade Service

**Files:**
- Create: `internal/cascade/service.go`
- Create: `internal/cascade/lexical.go`
- Test: `internal/cascade/service_test.go`
- Test: `internal/cascade/lexical_test.go`

- [ ] Write failing tests for cache hit, lexical gate fallback, untrusted seed fallback, Stage 2 write-back, and batch ordering.
- [ ] Implement cache/embedder/pipeline interfaces and `Service.Convert`.
- [ ] Run `go test ./internal/cascade`.

### Task 3: HTTP API

**Files:**
- Create: `internal/api/handler.go`
- Test: `internal/api/handler_test.go`
- Create: `cmd/iso-api/main.go`

- [ ] Write failing handler tests for single request, batch request, unknown public fields, and bad method.
- [ ] Implement strict JSON handler and server wiring.
- [ ] Run `go test ./internal/api ./cmd/iso-api`.

### Task 4: Cache And Embeddings

**Files:**
- Create: `internal/cache/types.go`
- Create: `internal/cache/postgres.go`
- Create: `internal/embedding/openai.go`
- Test: `internal/cache/postgres_test.go`
- Test: `internal/embedding/openai_test.go`
- Create: `sql/migrations/000001_create_address_cache.sql`

- [ ] Write failing tests for vector literal formatting and embedding response parsing.
- [ ] Implement pgvector search/upsert and OpenAI-compatible embedding client.
- [ ] Add migration for `address_cache` with `source` provenance and pgvector indexes.
- [ ] Run `go test ./internal/cache ./internal/embedding`.

### Task 5: Cache Fill Command And Docs

**Files:**
- Create: `cmd/iso-cache-fill/main.go`
- Modify: `README.md`
- Modify: `Taskfile.yml`
- Modify: `Makefile`
- Create: `examples/README.md`
- Create: `examples/go/client/main.go`

- [ ] Write command tests for flag validation where practical.
- [ ] Implement offline Stage 2 cache fill and ambiguous-row review export.
- [ ] Document `/convert`, cache fill, environment variables, and tasks.
- [ ] Run `go test ./...`.

### Task 6: Verification And Publishing

**Files:**
- Modify: generated gofmt changes only.

- [ ] Run `gofmt -w` on changed Go files.
- [ ] Run `go mod tidy`.
- [ ] Run `task verify`.
- [ ] Commit with `feat(api): add convert cascade service`.
- [ ] Push `main` to `origin`.
