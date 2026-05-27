CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS address_cache (
    id bigserial PRIMARY KEY,
    raw_address text NOT NULL,
    normalized_address text NOT NULL UNIQUE,
    embedding vector NOT NULL,
    source text NOT NULL CHECK (source IN ('human_verified', 'crf_pipeline', 'llm_assisted', 'sonnet_seed')),
    structured jsonb NOT NULL,
    country_code text,
    town text,
    postal_code text,
    street text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS address_cache_normalized_trgm_idx
    ON address_cache USING gin (normalized_address gin_trgm_ops);

CREATE INDEX IF NOT EXISTS address_cache_source_idx
    ON address_cache (source);

-- Add a vector index after choosing a fixed embedding dimension/model, for example:
-- CREATE INDEX address_cache_embedding_cos_384_idx
--     ON address_cache USING ivfflat ((embedding::vector(384)) vector_cosine_ops) WITH (lists = 100);
