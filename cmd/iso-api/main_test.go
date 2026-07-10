package main

import (
	"bytes"
	"testing"
)

func TestParseArgsAcceptsPostalDBPath(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseArgs([]string{"--postal-db", "/tmp/postal.db"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.postalDBPath != "/tmp/postal.db" {
		t.Fatalf("postalDBPath = %q, want /tmp/postal.db", opts.postalDBPath)
	}
}

func TestParseArgsReadsCacheBackendFlags(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseArgs(nil, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.cacheBackend != "pgvector" {
		t.Fatalf("default cacheBackend = %q, want pgvector", opts.cacheBackend)
	}

	opts, err = parseArgs([]string{"--cache-backend", "redis", "--redis-url", "redis://localhost:6379/0"}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.cacheBackend != "redis" {
		t.Fatalf("cacheBackend = %q, want redis", opts.cacheBackend)
	}
	if opts.redisURL != "redis://localhost:6379/0" {
		t.Fatalf("redisURL = %q", opts.redisURL)
	}
}
