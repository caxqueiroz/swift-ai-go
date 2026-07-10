package cache

import (
	"context"
	"fmt"
	"strings"
)

const (
	BackendPgvector = "pgvector"
	BackendRedis    = "redis"
)

// Config selects and configures the Stage 1 semantic cache backend.
type Config struct {
	Backend     string // BackendPgvector (default) or BackendRedis
	DatabaseURL string // pgvector backend
	RedisURL    string // redis backend
	Dimensions  int    // redis vector dimensions; <= 0 means DefaultRedisDimensions
}

// OpenStore opens the configured cache backend. It returns a nil Store with a
// no-op closer when the selected backend's URL is empty, which disables
// Stage 1 and keeps the caller serving through the live pipeline.
func OpenStore(ctx context.Context, cfg Config) (Store, func(), error) {
	noop := func() {}
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		backend = BackendPgvector
	}
	switch backend {
	case BackendPgvector:
		if strings.TrimSpace(cfg.DatabaseURL) == "" {
			return nil, noop, nil
		}
		store, err := NewPostgresStore(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, noop, err
		}
		return store, store.Close, nil
	case BackendRedis:
		if strings.TrimSpace(cfg.RedisURL) == "" {
			return nil, noop, nil
		}
		store, err := NewRedisStore(ctx, cfg.RedisURL, cfg.Dimensions)
		if err != nil {
			return nil, noop, err
		}
		return store, store.Close, nil
	default:
		return nil, noop, fmt.Errorf("unsupported cache backend %q (valid: %s, %s)", cfg.Backend, BackendPgvector, BackendRedis)
	}
}
