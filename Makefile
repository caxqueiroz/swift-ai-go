.PHONY: test build build-api build-cache-fill lint tidy serve migrate-up

test:
	go test ./...

build:
	go build ./cmd/iso-run
	go build ./cmd/iso-api
	go build ./cmd/iso-cache-fill

build-api:
	go build ./cmd/iso-api

build-cache-fill:
	go build ./cmd/iso-cache-fill

serve:
	go run ./cmd/iso-api

migrate-up:
	psql "$$DATABASE_URL" -f sql/migrations/000001_create_address_cache.sql

lint:
	golangci-lint run ./...

tidy:
	go mod tidy
