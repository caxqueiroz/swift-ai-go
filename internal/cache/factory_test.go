package cache

import (
	"context"
	"strings"
	"testing"
)

func TestOpenStoreDisablesStage1WhenURLEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "default backend without database URL", cfg: Config{}},
		{name: "explicit pgvector without database URL", cfg: Config{Backend: "pgvector"}},
		{name: "redis without redis URL", cfg: Config{Backend: "redis"}},
		{name: "redis ignores database URL", cfg: Config{Backend: "redis", DatabaseURL: "postgres://ignored"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, closeStore, err := OpenStore(context.Background(), tt.cfg)
			if err != nil {
				t.Fatalf("OpenStore() error = %v", err)
			}
			if store != nil {
				t.Fatal("OpenStore() must return a nil store when the backend URL is empty")
			}
			closeStore() // must not panic
		})
	}
}

func TestOpenStoreRejectsUnknownBackend(t *testing.T) {
	_, closeStore, err := OpenStore(context.Background(), Config{Backend: "memcached"})
	if err == nil {
		t.Fatal("OpenStore() must reject an unknown backend")
	}
	if !strings.Contains(err.Error(), "pgvector") || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("error must list valid backends, got %q", err.Error())
	}
	if closeStore == nil {
		t.Fatal("closeStore must be non-nil even on error")
	}
}
