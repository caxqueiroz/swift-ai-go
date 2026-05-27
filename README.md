# ISO 20022 Address Structuring Go Port

This repository is a Go port of `Swift-SC/iso20022-address-structuring`. It contains the address cleaning, CRF decoding, fuzzy GeoNames matching, postcode matching, post-processing, a batch CLI, an HTTP `/convert` API, and an offline semantic-cache fill command.

The upstream resource files and trained model weights are not vendored here. You need the original resource directory and a converted ONNX model before running full inference.

## What It Does

- Converts free-form postal address text into structured fields.
- Resolves country and town through the pipeline. API callers do not pass country hints.
- Keeps the API fast by checking trusted cache rows before live inference.
- Uses the Swift/CRF + GeoNames pipeline as the live resolver on cache misses.
- Uses LLMs only in offline cache fill, as constrained judges over GeoNames-backed candidates.
- Writes live or batch Swift results back into the cache as `crf_pipeline`.
- Supports confidence banding, LLM-assisted cache fill, and ambiguous-row review export.

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

`POST /convert` accepts free text only. Do not send `suggested_country` or `force_suggested_country`; country and town are resolved by trusted cache or the Swift/CRF + GeoNames pipeline.

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
DATABASE_URL=postgres://user:pass@localhost:5432/swift_ai?sslmode=disable \
OPENAI_API_KEY=$OPENAI_API_KEY \
EMBEDDING_MODEL=text-embedding-model \
go run ./cmd/iso-api --model-dir resources/models
```

If `DATABASE_URL`, `OPENAI_API_KEY`, or `EMBEDDING_MODEL` is missing, the API still works through live Swift/CRF + GeoNames inference and disables semantic cache lookup.

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
      "resolution_status": "resolved",
      "confidence_band": "high",
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

`resolution_status` is:

- `resolved`: country and town are present and above the high-confidence threshold.
- `partial`: only country or town is present.
- `needs_review`: complete but below the high threshold, or too weak to trust.

`confidence_band` is:

- `high`: default `>= 0.95`
- `medium`: default `>= 0.80` and `< 0.95`
- `low`: below `0.80`, or missing both country and town

## Serving Cascade

The online runtime flow is optimized for low latency:

```text
Request text
  -> normalize text
  -> exact normalized trusted cache lookup
  -> semantic cache lookup in pgvector, if exact miss
  -> cosine + lexical + provenance gates
  -> serve trusted near-exact cache hit
  -> otherwise run Swift/CRF + GeoNames live
  -> write live result back as crf_pipeline when embedding is available
  -> response
```

The cache is not the resolver. It is a near-exact memoization layer. A semantic cache row serves only when:

- semantic score is at least `--semantic-threshold` (default `0.90`)
- lexical identity is at least `--lexical-threshold` (default `0.85`)
- source provenance is trusted

Any miss, low lexical score, low semantic score, untrusted source, embedding error, or cache error falls through to live Swift/CRF + GeoNames. The online API does not call an LLM by default.

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
| `JUDGE_API_KEY` | Cache fill only | API key for offline LLM judge. Defaults to `OPENAI_API_KEY`. |
| `JUDGE_BASE_URL` | Cache fill only | Judge API base URL. Defaults to `OPENAI_BASE_URL`. |
| `JUDGE_MODEL` | Cache fill only | LLM judge model name. |
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

Use `iso-cache-fill` to run the Swift/CRF + GeoNames pipeline over a corpus and write high-confidence outputs into `address_cache` as `crf_pipeline`. Rows below the high threshold can be sent to an offline LLM judge.

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
go run ./cmd/iso-cache-fill \
  --input-path testdata/parity/addresses.csv \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models \
  --database-url "$DATABASE_URL" \
  --embedding-api-key "$OPENAI_API_KEY" \
  --embedding-model "$EMBEDDING_MODEL" \
  --enable-llm-judge \
  --judge-model "$JUDGE_MODEL" \
  --review-path /tmp/address-review.json \
  --high-confidence-threshold 0.95 \
  --medium-confidence-threshold 0.80
```

Useful flags:

| Flag | Purpose |
|------|---------|
| `--dry-run` | Run Stage 2 and review export without writing cache. |
| `--high-confidence-threshold` | Minimum country/town confidence for direct `crf_pipeline` cache writes. Default `0.95`. |
| `--medium-confidence-threshold` | Lower band boundary for uncertain but potentially judgeable rows. Default `0.80`. |
| `--enable-llm-judge` | Send non-high-confidence rows to the constrained LLM judge. |
| `--judge-model` | LLM model used for offline judging. |
| `--min-cache-confidence` | Optional extra floor below which cache writes are skipped. |
| `--review-threshold` | Legacy review-export confidence threshold when no judge is configured. |
| `--review-path` | JSON file for ambiguous rows. |

The cache-fill path does not treat LLM output as truth. The recommended flow is:

1. Run Swift/CRF + GeoNames over the corpus.
2. Cache high-confidence rows as `crf_pipeline`.
3. Send non-high-confidence rows to an LLM judge only with GeoNames-backed country/town candidates.
4. Reject any LLM answer that invents a country/town or mismatches town-country.
5. Cache valid LLM-reviewed rows as `llm_assisted`.
6. Export unresolved rows for human review.
7. Promote rows to `human_verified` only after human review.

## Parity Check

The parity test is gated because it needs upstream resources, a converted ONNX model, and Python-produced expected output.

```bash
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
ISO20022_MODEL_DIR=resources/models \
ISO20022_EXPECTED_PARITY_JSON=testdata/parity/expected_python_output.json \
go test ./internal/pipeline -run TestParityAgainstPythonFixtures -count=1
```

To refresh expected results, run the upstream Python CLI against `testdata/parity/addresses.csv`, then map each row to the expected top `country` and `town` fields in `testdata/parity/expected_python_output.json`.
