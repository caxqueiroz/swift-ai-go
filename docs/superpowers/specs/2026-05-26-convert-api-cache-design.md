# Convert API And Cache Design

## Goal

Expose a text-only `/convert` API that turns free-form address text into structured ISO20022 address fields. The endpoint must infer country and town itself; callers do not provide `suggested_country` or `force_suggested_country`.

## Public API

`POST /convert` accepts either one address:

```json
{ "text": "77 RUE DE RIVOLI 75001 PARIS" }
```

or a batch:

```json
{
  "items": [
    { "text": "77 RUE DE RIVOLI 75001 PARIS" },
    { "text": "350 FIFTH AVENUE NEW YORK NY 10118" }
  ]
}
```

The response always returns `items` in input order. Each item includes the original input, structured fields, and provenance:

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
        "street": "RUE DE RIVOLI"
      },
      "served_by": "stage2_pipeline",
      "cache_source": "crf_pipeline",
      "semantic_score": null,
      "lexical_score": null,
      "fallback_reason": "cache_miss"
    }
  ]
}
```

## Serving Cascade

Stage 1 is semantic search over a Postgres/pgvector cache. It is only a latency optimization. A Stage 1 row can serve the request only when all gates pass:

- cosine similarity is at or above the configured semantic threshold, default `0.90`
- lexical identity is at or above the configured threshold, default `0.85`
- cache source is trusted by policy

Trusted-by-default sources are `human_verified` and `crf_pipeline`. `sonnet_seed` and `llm_assisted` are stored as lower-trust seeds and fall through unless explicitly enabled after eval results justify it.

Stage 2 is the authoritative resolver: the existing CRF + fuzzy GeoNames + postcode + postprocess pipeline. Any cache miss, lexical gate failure, semantic mismatch, untrusted provenance, or Stage 1 dependency failure falls through to Stage 2.

## Cache Write-Back And Fill

When Stage 2 runs successfully, its structured result is written back to the cache with `source=crf_pipeline`, overwriting older `sonnet_seed` rows for the same normalized address. This makes the cache converge toward Stage 2 outputs.

Bulk cache fill uses the same rule: run the Stage 2 pipeline over an input corpus, write high-confidence results as `crf_pipeline`, and export ambiguous rows for human or LLM-assisted review. An LLM may be used only as a constrained judge over GeoNames-backed candidates, not as a free-form source of truth. LLM-assisted rows keep `source=llm_assisted` until eval or human verification promotes them.

## Components

- `cmd/iso-api`: HTTP server exposing `/convert`
- `cmd/iso-cache-fill`: offline cache fill command
- `internal/api`: JSON request/response handling
- `internal/cascade`: Stage 1/Stage 2 orchestration and provenance policy
- `internal/cache`: cache contracts and Postgres/pgvector implementation
- `internal/embedding`: OpenAI-compatible embedding client
- `internal/structured`: conversion from `core.Result` to stable API/cache structured fields
- `sql/migrations`: pgvector-backed cache schema

## Error Handling

Invalid JSON, missing text, unsupported methods, and unknown request fields return structured `4xx` JSON errors. Stage 2 failures return `500`. Cache lookup and cache write-back errors do not fail conversion if Stage 2 can produce an answer; they are represented as fallback reasons and should be logged by the server.

## Testing

Tests cover request validation, single and batch response shape, Stage 1 cache acceptance, lexical gate fallback, untrusted seed fallback, Stage 2 write-back, structured extraction of country/town/postal/street, and embedding client parsing.
