.PHONY: test build lint tidy

test:
	go test ./...

build:
	go build ./cmd/iso-run

lint:
	golangci-lint run ./...

tidy:
	go mod tidy
