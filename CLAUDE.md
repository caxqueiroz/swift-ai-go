# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go port of `Swift-SC/iso20022-address-structuring`: converts free-form postal address text into structured ISO 20022 fields (country, town, postal code, street). Serving is a two-stage cascade — a Postgres/pgvector semantic cache in front of a live CRF + GeoNames pipeline. LLMs are used only offline (cache fill judging), never on the online path.

Upstream resource files and trained model weights are **not vendored**. Full inference needs the upstream ISO20022 resources directory, a converted ONNX model (`tools/convert-model/export_onnx.py`), and the ONNX Runtime shared library (`ISO20022_ONNX_RUNTIME` env var; `task onnxruntime:download-darwin-arm64` on macOS arm64).

## Commands

Task is the primary runner (`task --list`); a Makefile mirrors the basics. The Taskfile pins Go caches to `/private/tmp/swift-ai-*` — prefer `task` over raw `go` commands for consistency with CI-like behavior, but plain `go test ./...` works too.

```bash
task test          # go test ./...
task lint          # golangci-lint run ./...
task verify        # test + lint
task build         # build iso-run, iso-api, iso-cache-fill into bin/
task generate      # sqlc generate (after editing sql/queries or migrations)

# Single test
go test -run TestName ./internal/<pkg>

# Local cache database (pg0 helper)
task pg0:start && task pg0:migrate   # postgresql://postgres:postgres@127.0.0.1:5432/swift_ai

# Serve API (no DATABASE_URL = Stage 1 cache disabled, live pipeline only)
go run ./cmd/iso-api --model-dir resources/models

# Offline cache fill / evaluation (see docs/offline-cache-and-evaluation.md)
task cache-fill:dataaddr-local COUNTRY=SG MAX_RECORDS=100 ...
task eval:dataaddr-client COUNTRY=SG LIMIT=1000 ...
task eval:dataaddr-sweep / eval:dataaddr-inventory
```

### Gated tests

Most tests run hermetically. Integration/parity tests skip unless env vars are set:

```bash
# Python parity (needs upstream resources + converted model + expected fixture)
ISO20022_RESOURCES_DIR=... ISO20022_MODEL_DIR=resources/models \
ISO20022_EXPECTED_PARITY_JSON=testdata/parity/expected_python_output.json \
go test ./internal/pipeline -run TestParityAgainstPythonFixtures -count=1
```

`internal/model/onnx_integration_test.go` similarly needs a real ONNX Runtime and model.

## Architecture

Three binaries share the same core:

- `cmd/iso-run` — batch CLI (file in/out; supports `suggested_country` hint columns, CLI-only)
- `cmd/iso-api` — HTTP `POST /convert` (free text only, no country hints; always returns `items`)
- `cmd/iso-cache-fill` — offline corpus ingestion into the semantic cache

### Serving cascade (`internal/cascade`)

The request path: normalize → exact trusted-cache lookup → pgvector semantic lookup → gates → serve, else fall through to the live pipeline and write the result back as `crf_pipeline`.

The cache is a **near-exact memoization layer, not a resolver**. A semantic hit serves only if it passes all three gates: semantic score ≥ threshold (default 0.90), lexical identity ≥ threshold (default 0.85), and trusted provenance (`human_verified` or `crf_pipeline` by default; `llm_assisted`/`sonnet_seed` are not served directly). Any gate failure, embedding error, or cache error falls through to Stage 2 — fall-through reasons are enumerated as `Fallback*` constants in `internal/cascade/service.go`.

### Live pipeline (`internal/pipeline`)

Orchestrates four batch runners behind interfaces (all mockable in tests):

```
text → ModelRunner (internal/model: char tokenizer → ONNX transformer → CRF decode → spans)
     → FuzzyRunner (internal/fuzzy: GeoNames country/town matching)
     → PostcodeRunner (internal/postcode)
     → PostprocessRunner (internal/postprocess: candidate combinations + final scoring)
     → core.Result
```

`internal/core` holds the shared types passed between stages. `internal/structured` maps pipeline results to the stable API/cache output shape. `internal/quality` computes `resolution_status` (resolved/partial/needs_review) and `confidence_band` (high ≥ 0.95, medium ≥ 0.80, low) — these bands drive both API responses and cache-fill write decisions.

### Embeddings (`internal/embedding`)

Two backends selected by `EMBEDDING_BACKEND`: `onnx` (in-process MiniLM, 384-dim, artifacts via `task embeddings:download-minilm`) or `openai` (HTTP, OpenAI-compatible).

### Cache & database

- Schema: `sql/migrations/000001_create_address_cache.sql` (pgvector + pg_trgm)
- Queries: `sql/queries/address_cache.sql` → sqlc generates `internal/cache/cachedb` (do not edit; run `task generate`)
- Provenance sources ordered by trust: `human_verified` > `crf_pipeline` > `llm_assisted` > `sonnet_seed`

### Offline cache fill (`cmd/iso-cache-fill`, `internal/judge`, `internal/readers`, `internal/holdout`)

Runs the pipeline over a corpus (`.txt`/`.csv`/`.tsv`/`.geojson`, or an OpenAddresses/DataAddr directory). High-confidence rows are cached as `crf_pipeline`; lower bands optionally go to a constrained LLM judge (`internal/judge`) that may only pick from GeoNames-backed candidates — invented or mismatched answers are rejected. `internal/holdout` deterministically excludes a bucket of rows from cache fill so evaluation measures resolver correctness, not cache lookup. The same holdout flags exist on the evaluator (`examples/client`), which samples DataAddr SQLite, calls `/convert`, and reports raw accuracy, coverage, and precision@committed.

Key docs: `docs/offline-cache-and-evaluation.md` (runbook), `docs/evaluation-plan.md` (metrics: the SLO target is precision@committed on `resolved`+`high` rows, not raw accuracy).

## Conventions

- Country hints (`suggested_country`, `force_suggested_country`) are batch/CLI/offline-only. The public `/convert` API accepts free text only — do not add hint parameters to it.
- DataAddr country folder names are used offline only as GeoNames disambiguation hints.
- The evaluator intentionally ignores postcode (sparse in the SQLite ground truth); DataAddr `town` labels are weak ground truth.
