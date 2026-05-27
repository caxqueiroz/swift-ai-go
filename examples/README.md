# Convert API Examples

Start the API:

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
EMBEDDING_MODEL=text-embedding-model \
OPENAI_API_KEY=$OPENAI_API_KEY \
DATABASE_URL=postgres://user:pass@localhost:5432/swift_ai?sslmode=disable \
go run ./cmd/iso-api
```

The public request is text-only. Do not send country hints; trusted cache rows or the Swift/CRF + GeoNames pipeline resolve country and town. The online API does not call an LLM by default.

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"text":"77 RUE DE RIVOLI 75001 PARIS"}'
```

Batch:

```bash
curl -s http://localhost:8080/convert \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"text":"77 RUE DE RIVOLI 75001 PARIS"},{"text":"350 FIFTH AVENUE NEW YORK NY 10118"}]}'
```

Required environment:

- `ISO20022_ONNX_RUNTIME`: ONNX Runtime shared library
- `ISO20022_RESOURCES_DIR`: upstream resource directory
- `DATABASE_URL`: Postgres database with pgvector migration applied
- `OPENAI_API_KEY`: embedding provider key
- `EMBEDDING_MODEL`: embedding model name

If `DATABASE_URL`, `OPENAI_API_KEY`, or `EMBEDDING_MODEL` is missing, `/convert` still runs through Stage 2 but Stage 1 semantic cache is disabled.

For offline cache filling with an LLM judge:

```bash
go run ./cmd/iso-cache-fill \
  --input-path addresses.csv \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models \
  --database-url "$DATABASE_URL" \
  --embedding-api-key "$OPENAI_API_KEY" \
  --embedding-model "$EMBEDDING_MODEL" \
  --enable-llm-judge \
  --judge-model "$JUDGE_MODEL" \
  --review-path /tmp/address-review.json
```

The judge can only choose from GeoNames-backed candidates, and valid judged rows are cached as `llm_assisted`.

## DataAddr SQLite Evaluation Client

`examples/client` reads a stratified sample from the DataAddr SQLite database, calls `POST /convert`, and compares only country and town against the SQLite labels.

Run the API without `DATABASE_URL` to measure the live Swift/CRF + GeoNames resolver:

```bash
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api --resources-dir resources --model-dir resources/models
```

Then evaluate a country sample:

```bash
go run ./examples/client \
  --sqlite /Volumes/cax-t7/Data/DataAddr/addresses.sqlite \
  --api-url http://localhost:8080/convert \
  --country SG \
  --limit 1000 \
  --mismatches /tmp/convert-mismatches-sg.jsonl
```

Or use Task:

```bash
task eval:dataaddr-client COUNTRY=SG LIMIT=1000 MISMATCHES=/tmp/convert-mismatches-sg.jsonl
```

Run the API with `DATABASE_URL` only when measuring cache behavior. If the same SQLite rows were used to build the cache, keep a holdout sample for evaluation; otherwise the test measures cache lookup, not resolver quality.
