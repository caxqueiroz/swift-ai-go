-- name: LookupAddressCacheByNormalized :one
SELECT
    raw_address,
    normalized_address,
    structured,
    source
FROM address_cache
WHERE normalized_address = $1;

-- name: SearchAddressCache :many
SELECT
    raw_address,
    normalized_address,
    structured,
    source,
    (1 - (embedding <=> sqlc.arg(embedding)::vector))::double precision AS semantic_score
FROM address_cache
ORDER BY embedding <=> sqlc.arg(embedding)::vector
LIMIT sqlc.arg(result_limit);

-- name: UpsertAddressCache :exec
INSERT INTO address_cache (
    raw_address,
    normalized_address,
    embedding,
    source,
    structured,
    country_code,
    town,
    postal_code,
    street
) VALUES (
    sqlc.arg(raw_address),
    sqlc.arg(normalized_address),
    sqlc.arg(embedding)::vector,
    sqlc.arg(source),
    sqlc.arg(structured)::jsonb,
    sqlc.narg(country_code),
    sqlc.narg(town),
    sqlc.narg(postal_code),
    sqlc.narg(street)
)
ON CONFLICT (normalized_address) DO UPDATE SET
    raw_address = EXCLUDED.raw_address,
    embedding = EXCLUDED.embedding,
    source = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.source
        ELSE EXCLUDED.source
    END,
    structured = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.structured
        ELSE EXCLUDED.structured
    END,
    country_code = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.country_code
        ELSE EXCLUDED.country_code
    END,
    town = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.town
        ELSE EXCLUDED.town
    END,
    postal_code = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.postal_code
        ELSE EXCLUDED.postal_code
    END,
    street = CASE
        WHEN address_cache.source = 'human_verified' AND EXCLUDED.source <> 'human_verified' THEN address_cache.street
        ELSE EXCLUDED.street
    END,
    updated_at = now();
