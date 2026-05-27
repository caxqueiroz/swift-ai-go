# ISO 20022 Address Structuring Go Port

This repository is a Go port of `Swift-SC/iso20022-address-structuring`. It contains the address cleaning, CRF decoding, fuzzy GeoNames matching, postcode matching, post-processing, a batch CLI, an HTTP `/convert` API, and an offline semantic-cache fill command.

The upstream resource files and trained model weights are not vendored here. You need the original resource directory and a converted ONNX model before running full inference.

## What It Does

- Converts free-form postal address text into structured fields.
- Resolves country and town through the pipeline. API callers do not pass country hints.
- Uses Stage 1 semantic search only as a gated cache lookup.
- Uses Stage 2 CRF + GeoNames as the authoritative resolver.
- Writes Stage 2 results back into the cache as `crf_pipeline`.
- Supports offline cache fill and ambiguous-row review export.

## Layout

| Path | Purpose |
|------|---------|
| `cmd/iso-run` | Batch CLI for file input/output. |
| `cmd/iso-api` | HTTP server exposing `POST /convert`. |
| `cmd/iso-cache-fill` | Offline command to fill the semantic cache from a corpus. |
| `internal/api` | JSON request/response handling. |
| `internal/cascade` | Stage 1 cache gate, Stage 2 fallback, provenance policy, write-back. |
| `internal/cache` | Postgres/pgvector cache implementation. |
| `internal/embedding` | OpenAI-compatible embedding client. |
| `internal/structured` | Stable API/cache structured address output. |
| `internal/pipeline` | End-to-end CRF + fuzzy + postcode + postprocess orchestration. |
| `internal/model` | Character tokenizer, ONNX adapter, CRF decoding, span grouping. |
| `internal/fuzzy`, `internal/postcode`, `internal/postprocess` | Matching and final scoring. |
| `internal/resources` | Resource file loading. |
| `sql/migrations` | Database schema for semantic cache. |
| `tools/convert-model` | PyTorch-to-ONNX conversion script. |
| `testdata/parity` | Small gated parity fixtures. |

## Prerequisites

- Go `1.26.3`
- ONNX Runtime shared library
- Upstream ISO20022 resource directory
- Converted model files under `resources/models`
- Optional for Stage 1: Postgres with `pgvector`
- Optional for Stage 1: OpenAI-compatible embedding endpoint

`internal/model` uses `github.com/yalue/onnxruntime_go`. For real inference, install ONNX Runtime and set `ISO20022_ONNX_RUNTIME` if the shared library is not discoverable by default.

## Commands

```bash
task test          # go test ./...
task lint          # golangci-lint run ./...
task verify        # tests + lint
task build         # build iso-run, iso-api, and iso-cache-fill
task build-api     # build only iso-api
task build-cache-fill
```

Equivalent Make targets are available:

```bash
make test
make build
make serve
make migrate-up
make lint
```

## Convert The Model

```bash
python3 tools/convert-model/export_onnx.py \
  --source-root /path/to/iso20022-address-structuring \
  --weights /path/to/resources/models/CRF_with_MLP_EPOCH_1.safetensors \
  --config /path/to/resources/models/CRF_with_MLP_EPOCH_1.config.json \
  --output-dir resources/models
```

This writes:

- `resources/models/address_transformer.onnx`
- `resources/models/address_transformer.config.json`
- `resources/models/address_crf.json`

## Run The Batch CLI

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
go run ./cmd/iso-run \
  --input-path testdata/parity/addresses.csv \
  --output-path /tmp/iso20022-output.json \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models
```

Input supports `.txt`, `.csv`, and `.tsv`. Delimited input expects an `address` column and may include `suggested_country` and `force_suggested_country`. These country-hint columns are for batch/CLI workflows only, not the public `/convert` API.

Output supports `.csv`, `.tsv`, and `.json`. Pass `--verbose` to include CRF emissions and log probabilities in JSON output.

## Run The Convert API

`POST /convert` accepts free text only. Do not send `suggested_country` or `force_suggested_country`; country and town are resolved by Stage 1 cache or Stage 2 pipeline.

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
DATABASE_URL=postgres://user:pass@localhost:5432/swift_ai?sslmode=disable \
OPENAI_API_KEY=$OPENAI_API_KEY \
EMBEDDING_MODEL=text-embedding-model \
go run ./cmd/iso-api --model-dir resources/models
```

If `DATABASE_URL`, `OPENAI_API_KEY`, or `EMBEDDING_MODEL` is missing, the API still works through Stage 2 and disables Stage 1 semantic cache.

### Single Request

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"text":"77 RUE DE RIVOLI 75001 PARIS"}'
```

### Batch Request

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"text":"77 RUE DE RIVOLI 75001 PARIS"},{"text":"350 FIFTH AVENUE NEW YORK NY 10118"}]}'
```

### Response Shape

The API always returns `items`, even for a single request.

```json
{
  "items": [
    {
      "input": "77 RUE DE RIVOLI 75001 PARIS",
      "structured": {
        "address_line": "77 RUE DE RIVOLI 75001 PARIS",
        "country": "FR",
        "town": "PARIS",
        "postal_code": "75001",
        "street": "RUE DE RIVOLI",
        "country_confidence": 0.93,
        "town_confidence": 0.94
      },
      "served_by": "stage2_pipeline",
      "cache_source": "crf_pipeline",
      "fallback_reason": "cache_miss"
    }
  ]
}
```

`served_by` is either:

- `stage1_cache`
- `stage2_pipeline`

`cache_source` can be:

- `human_verified`
- `crf_pipeline`
- `llm_assisted`
- `sonnet_seed`

By default, only `human_verified` and `crf_pipeline` rows can serve directly from Stage 1.

## Serving Cascade

The runtime flow is:

```text
Request text
  -> normalize text
  -> Stage 1 semantic search in pgvector
  -> cosine gate
  -> lexical identity gate
  -> provenance gate
  -> serve trusted near-exact cache hit
  -> otherwise Stage 2 CRF + GeoNames pipeline
  -> write Stage 2 result back as crf_pipeline
  -> response
```

Stage 1 is not the resolver. It is a near-exact memoization layer. A cache row serves only when:

- semantic score is at least `--semantic-threshold` (default `0.90`)
- lexical identity is at least `--lexical-threshold` (default `0.85`)
- source provenance is trusted

Any miss, low lexical score, low semantic score, untrusted source, embedding error, or cache error falls through to Stage 2.

## Environment Variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `ISO20022_ONNX_RUNTIME` | Yes for inference | Path to ONNX Runtime shared library when not discoverable. |
| `ISO20022_RESOURCES_DIR` | Yes for inference | Upstream resources directory. |
| `DATABASE_URL` | Stage 1 only | Postgres connection string for semantic cache. |
| `OPENAI_API_KEY` | Stage 1 only | API key for OpenAI-compatible embeddings. |
| `OPENAI_BASE_URL` | No | Embedding API base URL. Defaults to `https://api.openai.com/v1`. |
| `EMBEDDING_MODEL` | Stage 1 only | Embedding model name. |
| `EMBEDDING_DIMENSIONS` | No | Optional embedding dimensions. |
| `PORT` | No | HTTP port. Defaults to `8080`. |
| `ADDR` | No | Full HTTP listen address, overrides `PORT`. |

## Database Migration

Apply the cache schema before enabling Stage 1:

```bash
psql "$DATABASE_URL" -f sql/migrations/000001_create_address_cache.sql
```

The migration creates:

- `address_cache`
- `pgvector` extension
- `pg_trgm` extension
- cosine vector index
- trigram index on normalized address
- source provenance index

## Fill The Semantic Cache

Use `iso-cache-fill` to run Stage 2 over a corpus and write outputs into `address_cache` as `crf_pipeline`.

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
go run ./cmd/iso-cache-fill \
  --input-path testdata/parity/addresses.csv \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models \
  --database-url "$DATABASE_URL" \
  --embedding-api-key "$OPENAI_API_KEY" \
  --embedding-model "$EMBEDDING_MODEL" \
  --review-path /tmp/address-review.json \
  --review-threshold 0.85
```

Useful flags:

| Flag | Purpose |
|------|---------|
| `--dry-run` | Run Stage 2 and review export without writing cache. |
| `--min-cache-confidence` | Skip cache writes below a country/town confidence threshold. |
| `--review-threshold` | Export rows below this confidence to review JSON. |
| `--review-path` | JSON file for ambiguous rows. |

The cache-fill path does not treat LLM output as truth. The recommended flow is:

1. Run Stage 2 over the corpus.
2. Cache high-confidence rows as `crf_pipeline`.
3. Export ambiguous rows for review.
4. Use an LLM only as a constrained judge over GeoNames-backed candidates.
5. Keep LLM-reviewed rows as `llm_assisted` unless a human promotes them to `human_verified`.

## Parity Check

The parity test is gated because it needs upstream resources, a converted ONNX model, and Python-produced expected output.

```bash
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
ISO20022_MODEL_DIR=resources/models \
ISO20022_EXPECTED_PARITY_JSON=testdata/parity/expected_python_output.json \
go test ./internal/pipeline -run TestParityAgainstPythonFixtures -count=1
```

To refresh expected results, run the upstream Python CLI against `testdata/parity/addresses.csv`, then map each row to the expected top `country` and `town` fields in `testdata/parity/expected_python_output.json`.
