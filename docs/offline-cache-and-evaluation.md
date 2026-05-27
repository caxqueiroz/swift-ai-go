# Offline Cache And Evaluation Runbook

This document explains the current address resolution architecture, how the offline cache is built, and how to evaluate it using the DataAddr SQLite datasets.

## Goals

The system has two separate goals:

1. Serve `/convert` quickly for repeated or near-identical addresses.
2. Keep wrong country/town answers as close to zero as practical.

The cache is a latency layer. It must not become a source of unverified truth.

## Dependency Boundary

Online serving and offline cache fill run as Go binaries. The only external runtime dependency for model inference is ONNX Runtime:

```text
Go binary
  -> Swift/CRF ONNX model
  -> MiniLM ONNX embedding model
  -> ONNX Runtime shared library
```

Python is used only as a one-time asset conversion or resource generation tool when preparing local `resources/`. It is not part of the API or cache-fill runtime.

## Runtime Architecture

```text
POST /convert
  -> normalize input text
  -> Stage 1: trusted cache lookup
       exact normalized lookup
       semantic pgvector lookup
       lexical identity gate
       source provenance gate
  -> Stage 2: Swift/CRF + GeoNames pipeline
  -> write Stage 2 result back as crf_pipeline when embedding is available
  -> JSON response with resolution_status and confidence_band
```

Stage 1 is semantic search, but it is not allowed to serve on semantic similarity alone. A candidate must pass:

- semantic score: default `>= 0.90`
- lexical identity: default `>= 0.85`
- trusted source: `human_verified` or `crf_pipeline` by default

`sonnet_seed` and `llm_assisted` rows are not served by default. They require explicit API flags once evaluated:

```bash
--trust-sonnet-seed
--trust-llm-assisted
```

## Data Sources

### GeoNames

GeoNames is the geographic authority used by Stage 2. It provides normalized country/town candidates, alternate names, and country/town relationships.

GeoNames is used to answer questions like:

- Is this country real?
- Is this town real?
- Which country does this town belong to?
- Is this town spelling an alias?
- Is the same town name ambiguous across countries?

Relevant generated files live under `resources/`:

```text
resources/towns_all_countries.parquet
resources/town_aliases.json
resources/country_names.json
resources/country_province_names.json
resources/post_codes/*.json
resources/misc/country_specs.json
```

### DataAddr

DataAddr is a large curated address corpus. It is useful for coverage and offline cache generation, but it is not blindly trusted as the final ISO 20022 structured output.

DataAddr helps with:

- creating many realistic input strings
- filling cache rows for high-volume addresses
- stress-testing countries and hard strata
- providing weak labels for `country` and `town`
- creating review sets for LLM or human adjudication

DataAddr does not replace GeoNames or a human-verified eval set. Its `town` field can mean city, district, municipality, locality, suburb, or a source-specific label depending on country.

## Confidence Policy

The current default confidence bands are:

```text
high   >= 0.95
medium >= 0.80 and < 0.95
low    < 0.80
```

For low false positives, use a stricter offline direct-cache gate:

```text
>= 0.99
  cache directly as crf_pipeline

0.80 - 0.99
  send to constrained LLM judge or human review

< 0.80
  do not cache automatically
```

The confidence used by the API is not raw CRF probability only. It is the final postprocessed country/town score after Swift/CRF, fuzzy matching, GeoNames, postcode features, and relationship flags.

## Offline Cache Build

### One-Time Local Setup

```bash
task onnxruntime:download-darwin-arm64
task embeddings:download-minilm
task pg0:start
task pg0:migrate
task build-cache-fill
```

The local ONNX Runtime path is:

```text
.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib
```

The local MiniLM embedding artifacts are:

```text
resources/embeddings/all-MiniLM-L6-v2/model.onnx
resources/embeddings/all-MiniLM-L6-v2/vocab.txt
```

### Smoke Fill

Run a small country sample before a long run:

```bash
task cache-fill:dataaddr-local COUNTRY=CL MAX_RECORDS=100 \
  DATAADDR_DIR=/Volumes/cax-t7/Data/DataAddr \
  REVIEW_PATH=/tmp/address-review-cl-smoke.json
```

For a shard or another mount:

```bash
task cache-fill:dataaddr-local COUNTRY=CL MAX_RECORDS=100 \
  DATAADDR_DIR=/Volumes/cax-i7/Data/DataAddr \
  REVIEW_PATH=/tmp/address-review-cl-smoke.json
```

### Precision-First Cache Fill

Use this when the priority is reducing false positives:

```bash
DATABASE_URL=postgresql://postgres:postgres@127.0.0.1:5432/swift_ai \
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
./bin/iso-cache-fill \
  --embedding-backend onnx \
  --input-path /Volumes/cax-t7/Data/DataAddr \
  --country CL \
  --resources-dir resources \
  --model-dir resources/models \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80 \
  --review-path /tmp/address-review-cl.json
```

### With LLM Judge

The LLM judge is offline only. It is constrained to choose from GeoNames-backed candidates and should not invent country or town values.

```bash
JUDGE_API_KEY=... \
DATABASE_URL=postgresql://postgres:postgres@127.0.0.1:5432/swift_ai \
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
./bin/iso-cache-fill \
  --embedding-backend onnx \
  --input-path /Volumes/cax-t7/Data/DataAddr \
  --country CL \
  --resources-dir resources \
  --model-dir resources/models \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80 \
  --enable-llm-judge \
  --judge-model <model> \
  --review-path /tmp/address-review-cl.json
```

Valid judged rows are cached as `llm_assisted`. Do not serve them online until they are evaluated and the API is started with `--trust-llm-assisted`.

## Cache Database

Apply the migration:

```bash
task pg0:migrate
```

Inspect cached rows:

```bash
pg0 psql --name swift-ai-cache -- \
  -c "select source, count(*) from address_cache group by source order by source"
```

After choosing MiniLM as the standard embedding model, add a 384-dimensional vector index:

```bash
pg0 psql --name swift-ai-cache -- \
  -c "CREATE INDEX IF NOT EXISTS address_cache_embedding_cos_384_idx ON address_cache USING ivfflat ((embedding::vector(384)) vector_cosine_ops) WITH (lists = 100)"
```

## Running The API

### Resolver-Only Mode

Use this for model quality evaluation. Do not set `DATABASE_URL`.

```bash
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api \
  --resources-dir resources \
  --model-dir resources/models \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80
```

### Cache-Enabled Mode

Use this for cache behavior and latency tests.

```bash
DATABASE_URL=postgresql://postgres:postgres@127.0.0.1:5432/swift_ai \
EMBEDDING_BACKEND=onnx \
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
./bin/iso-api \
  --resources-dir resources \
  --model-dir resources/models \
  --semantic-threshold 0.90 \
  --lexical-threshold 0.85 \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80
```

Do not evaluate model accuracy against rows already loaded into the cache. That measures cache lookup, not resolver correctness.

## DataAddr SQLite Evaluator

The evaluator lives in `examples/client`.

It reads a stratified sample from a DataAddr SQLite database, calls `/convert`, and compares only:

- SQLite `country` vs API `structured.country`
- SQLite `town` vs API `structured.town`

Postcode is intentionally ignored because the SQLite files can be sparse for postcode.

### SQLite Schema

The expected table is:

```sql
CREATE TABLE addresses (
  id TEXT PRIMARY KEY,
  address TEXT NOT NULL,
  town TEXT,
  country TEXT NOT NULL,
  postcode TEXT
);
```

### Input Modes

The evaluator supports three input modes:

| Mode | Sent To `/convert` | Use |
|------|---------------------|-----|
| `address` | `address` | Tests whether town/country can be inferred from street-only text. Usually too strict for DataAddr shards. |
| `address-town` | `address + town` | Default. Tests town normalization and country inference when town appears in text. |
| `address-town-country` | `address + town + country` | Tests full normalization when the country appears in text. Useful for separating country inference failures from town matching failures. |

### Add Per-Shard Index

For completed shard files, add:

```bash
for c in ae ar at bg bm bs by ch cl cn; do
  sqlite3 /Volumes/cax-t7/Data/DataAddr/address_shards/$c.sqlite \
    "CREATE INDEX IF NOT EXISTS idx_addresses_eval_country_town_id ON addresses(country, town, id) WHERE town IS NOT NULL AND town <> '';"
done
```

### Run A Chile Sample

Resolver-only API:

```bash
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api --resources-dir resources --model-dir resources/models
```

Evaluator:

```bash
task eval:dataaddr-client \
  SQLITE=/Volumes/cax-t7/Data/DataAddr/address_shards/cl.sqlite \
  COUNTRY=CL \
  LIMIT=1000 \
  BATCH_SIZE=10 \
  INPUT_MODE=address-town-country \
  MISMATCHES=/tmp/convert-mismatches-cl.jsonl
```

Equivalent direct command:

```bash
go run ./examples/client \
  --sqlite /Volumes/cax-t7/Data/DataAddr/address_shards/cl.sqlite \
  --api-url http://localhost:8080/convert \
  --country CL \
  --limit 1000 \
  --batch-size 10 \
  --input-mode address-town-country \
  --mismatches /tmp/convert-mismatches-cl.jsonl
```

### Interpreting Results

The evaluator prints JSON:

```json
{
  "sampled": 10,
  "mismatches": 3,
  "input_mode": "address-town-country",
  "metrics": {
    "country_accuracy": 1,
    "town_accuracy": 0.7,
    "both_accuracy": 0.7
  }
}
```

Mismatch JSONL rows include enough context for review:

```json
{"id":"...","address":"6 LAS GAVIOTAS\nCURACO DE VELEZ\nCL","expected_country":"CL","expected_town":"CURACO DE VELEZ","actual_country":"CL","actual_town":"CURACO","status":"needs_review","band":"medium"}
```

Country mismatch is usually a strong signal. Town mismatch is a review signal because DataAddr `town` may not always use the same locality concept as GeoNames.

## Holdout Strategy

Do not fill the cache and evaluate on the same rows unless the goal is cache lookup testing.

Recommended split:

```text
80 percent
  offline cache fill

20 percent
  holdout evaluation
```

At minimum, keep a deterministic holdout by country and town stratum using the evaluator seed. A future improvement should make `iso-cache-fill` and `examples/client` share an explicit holdout manifest.

## Current Observations

Small resolver-only smoke tests showed:

- `CL` with `address-town-country`, 10 rows: country 100 percent, town 70 percent, both 70 percent.
- `CL` with `address-town`, 5 rows: town mostly matched, country often absent because country was not in input text.
- `AT` with `address-town`, 5 rows: many partial results; country often absent.
- `AE` street/numeric/Arabic-heavy samples returned mostly unresolved.
- `BM` has `Territory Wide` as the town label, which is not a useful town-level truth target.

These are smoke checks, not statistically meaningful eval results. Use larger stratified samples and review mismatches before changing trust thresholds.

## Recommended Operating Policy

1. Use `0.99` as the direct Swift/CRF cache threshold when optimizing for near-zero false positives.
2. Use `0.80` as the medium band for judge/review candidates.
3. Use LLMs offline only, constrained to GeoNames candidates.
4. Do not serve `llm_assisted` rows online until evaluated.
5. Use resolver-only API for model quality evaluation.
6. Use cache-enabled API only for cache hit and latency evaluation, or with a held-out sample.
7. Treat DataAddr labels as high-quality weak labels, not universal ground truth.
