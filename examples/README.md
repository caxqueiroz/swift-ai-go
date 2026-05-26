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

The public request is text-only. Do not send country hints; the pipeline resolves country and town.

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
