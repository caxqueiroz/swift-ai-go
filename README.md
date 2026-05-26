# ISO 20022 Address Structuring Go Port

This repository is a Go port of `Swift-SC/iso20022-address-structuring`. It contains the address cleaning, fuzzy matching, CRF decoding, post-processing, output writers, an `iso-run` CLI, and an `iso-api` HTTP service wired to ONNX Runtime.

The upstream resource files and trained model weights are not vendored here. Use the original resource directory and convert the PyTorch checkpoint before running full inference.

## Layout

- `cmd/iso-run`: batch CLI entrypoint.
- `cmd/iso-api`: `/convert` HTTP API.
- `cmd/iso-cache-fill`: offline semantic-cache fill command.
- `internal/api`, `internal/cascade`, `internal/cache`, `internal/embedding`, `internal/structured`: serving API, Stage 1 cache gate, pgvector cache, embeddings, and stable structured output.
- `internal/pipeline`: end-to-end orchestration.
- `internal/model`: character tokenizer, ONNX adapter, CRF decoding, and span grouping.
- `internal/fuzzy`, `internal/postcode`, `internal/postprocess`: matching and final scoring.
- `internal/resources`: resource file loading.
- `tools/convert-model`: PyTorch-to-ONNX conversion script.
- `testdata/parity`: small gated parity fixtures.

## Build And Test

```bash
go test ./...
go build ./cmd/iso-run
go build ./cmd/iso-api
go build ./cmd/iso-cache-fill
```

`internal/model` uses `github.com/yalue/onnxruntime_go`. For real inference, install ONNX Runtime and set `ISO20022_ONNX_RUNTIME` if the shared library is not discoverable by default.

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

## Run The CLI

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
go run ./cmd/iso-run \
  --input-path testdata/parity/addresses.csv \
  --output-path /tmp/iso20022-output.json \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models
```

Input supports `.txt`, `.csv`, and `.tsv`. Delimited input expects an `address` column and may include `suggested_country` and `force_suggested_country`. Output supports `.csv`, `.tsv`, and `.json`; pass `--verbose` to include CRF emissions and log probabilities in JSON output.

## Run The Convert API

`POST /convert` is text-only. Callers do not pass country hints; country and town are resolved by the semantic cache or the CRF + GeoNames pipeline.

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
DATABASE_URL=postgres://user:pass@localhost:5432/swift_ai?sslmode=disable \
OPENAI_API_KEY=$OPENAI_API_KEY \
EMBEDDING_MODEL=text-embedding-model \
go run ./cmd/iso-api --model-dir resources/models
```

Single request:

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"text":"77 RUE DE RIVOLI 75001 PARIS"}'
```

Batch request:

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"text":"77 RUE DE RIVOLI 75001 PARIS"},{"text":"350 FIFTH AVENUE NEW YORK NY 10118"}]}'
```

Stage 1 is semantic search against `address_cache`, gated by cosine similarity, lexical identity, and source provenance. If any gate fails, the service falls through to Stage 2. Stage 2 is the authoritative CRF + GeoNames resolver and writes successful results back with `source=crf_pipeline`.

Apply the pgvector migration before enabling Stage 1:

```bash
psql "$DATABASE_URL" -f sql/migrations/000001_create_address_cache.sql
```

If `DATABASE_URL`, `OPENAI_API_KEY`, or `EMBEDDING_MODEL` is missing, the API still serves requests through Stage 2 and disables Stage 1.

## Fill The Semantic Cache

Use `iso-cache-fill` to run Stage 2 over a corpus and write results into `address_cache` as `crf_pipeline`. Ambiguous rows can be exported for human or constrained LLM review.

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

The cache-fill path does not treat LLM output as truth. Use the review export for hard cases, constrain any LLM judge to GeoNames-backed candidates, and keep those rows as `llm_assisted` or `human_verified` according to review quality.

## Parity Check

The parity test is gated because it needs upstream resources, a converted ONNX model, and Python-produced expected output.

```bash
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
ISO20022_MODEL_DIR=resources/models \
ISO20022_EXPECTED_PARITY_JSON=testdata/parity/expected_python_output.json \
go test ./internal/pipeline -run TestParityAgainstPythonFixtures -count=1
```

To refresh expected results, run the upstream Python CLI against `testdata/parity/addresses.csv`, then map each row to the expected top `country` and `town` fields in `testdata/parity/expected_python_output.json`.
