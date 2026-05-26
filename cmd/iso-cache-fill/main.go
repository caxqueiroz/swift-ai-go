package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/cascade"
	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/embedding"
	"github.com/tipmarket/swift-ai/internal/pipeline"
	"github.com/tipmarket/swift-ai/internal/readers"
	isoruntime "github.com/tipmarket/swift-ai/internal/runtime"
	"github.com/tipmarket/swift-ai/internal/structured"
)

type fillOptions struct {
	inputPath           string
	resourcesDir        string
	modelDir            string
	databaseURL         string
	embeddingAPIKey     string
	embeddingBaseURL    string
	embeddingModel      string
	embeddingDimensions int
	batchSize           int
	minCacheConfidence  float64
	reviewThreshold     float64
	reviewPath          string
	dryRun              bool
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	opts, err := parseArgs(args, stderr)
	if err != nil {
		logger.Error("parse flags", "error", err)
		return 2
	}

	samples, err := readSamples(opts.inputPath)
	if err != nil {
		logger.Error("read input", "error", err)
		return 1
	}

	cfg := config.Default()
	cfg.BatchSize = opts.batchSize
	cfg.Database.PrefixFolderPath = opts.resourcesDir
	if opts.modelDir != "" {
		cfg.CRF.ModelPath = filepath.Join(opts.modelDir, "address_transformer.onnx")
		cfg.CRF.ModelConfigPath = filepath.Join(opts.modelDir, "address_transformer.config.json")
		cfg.CRF.CRFConfigPath = filepath.Join(opts.modelDir, "address_crf.json")
	}

	db, err := isoruntime.LoadDatabase(cfg)
	if err != nil {
		logger.Error("load resources", "error", err)
		return 1
	}
	modelRunner, engine, err := isoruntime.LoadModelRunner(cfg)
	if err != nil {
		logger.Error("load model", "error", err)
		return 1
	}
	defer func() {
		if err := engine.Close(); err != nil {
			logger.Error("close ONNX engine", "error", err)
		}
	}()

	results, err := pipeline.New(cfg, &db, modelRunner).Run(ctx, samples)
	if err != nil {
		logger.Error("run pipeline", "error", err)
		return 1
	}

	var store *cache.PostgresStore
	var embedder *embedding.OpenAICompatible
	if !opts.dryRun {
		store, embedder, err = fillDependencies(ctx, opts)
		if err != nil {
			logger.Error("configure cache fill", "error", err)
			return 1
		}
		defer store.Close()
	}

	summary, err := fillCache(ctx, samples, results, store, embedder, opts)
	if err != nil {
		logger.Error("fill cache", "error", err)
		return 1
	}
	if opts.reviewPath != "" {
		if err := writeReview(opts.reviewPath, summary.ReviewRows); err != nil {
			logger.Error("write review rows", "error", err)
			return 1
		}
	}

	if _, err := fmt.Fprintf(stdout, "processed=%d cached=%d review=%d skipped=%d\n",
		summary.Processed, summary.Cached, len(summary.ReviewRows), summary.Skipped); err != nil {
		logger.Error("write summary", "error", err)
		return 1
	}
	return 0
}

func parseArgs(args []string, stderr io.Writer) (fillOptions, error) {
	cfg := config.Default()
	opts := fillOptions{
		resourcesDir:        envDefault("ISO20022_RESOURCES_DIR", cfg.Database.PrefixFolderPath),
		databaseURL:         os.Getenv("DATABASE_URL"),
		embeddingAPIKey:     os.Getenv("OPENAI_API_KEY"),
		embeddingBaseURL:    envDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		embeddingModel:      os.Getenv("EMBEDDING_MODEL"),
		batchSize:           cfg.BatchSize,
		minCacheConfidence:  0,
		reviewThreshold:     0.85,
		embeddingDimensions: 0,
	}
	if value := os.Getenv("EMBEDDING_DIMENSIONS"); value != "" {
		dimensions, err := strconv.Atoi(value)
		if err != nil {
			return fillOptions{}, fmt.Errorf("parse EMBEDDING_DIMENSIONS: %w", err)
		}
		opts.embeddingDimensions = dimensions
	}

	fs := flag.NewFlagSet("iso-cache-fill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.inputPath, "input-path", "", "input file path")
	fs.StringVar(&opts.inputPath, "i", "", "input file path")
	fs.IntVar(&opts.batchSize, "batch-size", opts.batchSize, "pipeline batch size")
	fs.StringVar(&opts.resourcesDir, "resources-dir", opts.resourcesDir, "resource directory")
	fs.StringVar(&opts.modelDir, "model-dir", "", "model artifact directory")
	fs.StringVar(&opts.databaseURL, "database-url", opts.databaseURL, "Postgres connection string")
	fs.StringVar(&opts.embeddingAPIKey, "embedding-api-key", opts.embeddingAPIKey, "OpenAI-compatible embedding API key")
	fs.StringVar(&opts.embeddingBaseURL, "embedding-base-url", opts.embeddingBaseURL, "OpenAI-compatible base URL")
	fs.StringVar(&opts.embeddingModel, "embedding-model", opts.embeddingModel, "embedding model name")
	fs.IntVar(&opts.embeddingDimensions, "embedding-dimensions", opts.embeddingDimensions, "optional embedding dimensions")
	fs.Float64Var(&opts.minCacheConfidence, "min-cache-confidence", opts.minCacheConfidence, "minimum country/town confidence to cache")
	fs.Float64Var(&opts.reviewThreshold, "review-threshold", opts.reviewThreshold, "confidence threshold for review export")
	fs.StringVar(&opts.reviewPath, "review-path", "", "optional JSON path for ambiguous rows")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "run pipeline and review export without writing cache")
	if err := fs.Parse(args); err != nil {
		return fillOptions{}, err
	}
	if opts.inputPath == "" {
		return fillOptions{}, errors.New("input path is required")
	}
	if !opts.dryRun {
		if opts.databaseURL == "" {
			return fillOptions{}, errors.New("database URL is required unless --dry-run is set")
		}
		if opts.embeddingAPIKey == "" {
			return fillOptions{}, errors.New("embedding API key is required unless --dry-run is set")
		}
		if opts.embeddingModel == "" {
			return fillOptions{}, errors.New("embedding model is required unless --dry-run is set")
		}
	}
	return opts, nil
}

func readSamples(path string) ([]core.AddressSample, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return readers.ReadText(path)
	case ".csv":
		return readers.ReadDelimited(path, ',', readers.DefaultAddressColumn)
	case ".tsv":
		return readers.ReadDelimited(path, '\t', readers.DefaultAddressColumn)
	default:
		return nil, fmt.Errorf("unsupported input format %q", filepath.Ext(path))
	}
}

func fillDependencies(ctx context.Context, opts fillOptions) (*cache.PostgresStore, *embedding.OpenAICompatible, error) {
	store, err := cache.NewPostgresStore(ctx, opts.databaseURL)
	if err != nil {
		return nil, nil, err
	}
	embedder, err := embedding.NewOpenAICompatible(embedding.Config{
		APIKey:     opts.embeddingAPIKey,
		BaseURL:    opts.embeddingBaseURL,
		Model:      opts.embeddingModel,
		Dimensions: opts.embeddingDimensions,
	})
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return store, embedder, nil
}

type fillSummary struct {
	Processed  int
	Cached     int
	Skipped    int
	ReviewRows []reviewRow
}

type reviewRow struct {
	Input             string  `json:"input"`
	Country           string  `json:"country"`
	Town              string  `json:"town"`
	CountryConfidence float64 `json:"country_confidence"`
	TownConfidence    float64 `json:"town_confidence"`
	Reason            string  `json:"reason"`
}

func fillCache(ctx context.Context, samples []core.AddressSample, results []core.Result, store *cache.PostgresStore, embedder *embedding.OpenAICompatible, opts fillOptions) (fillSummary, error) {
	if len(samples) != len(results) {
		return fillSummary{}, fmt.Errorf("sample/result length mismatch: %d samples, %d results", len(samples), len(results))
	}

	summary := fillSummary{Processed: len(samples)}
	for i, result := range results {
		address := structured.FromResult(result)
		if reviewReason(address, opts.reviewThreshold) != "" {
			summary.ReviewRows = append(summary.ReviewRows, newReviewRow(samples[i].Text, address, reviewReason(address, opts.reviewThreshold)))
		}
		if !cacheable(address, opts.minCacheConfidence) {
			summary.Skipped++
			continue
		}
		if opts.dryRun {
			continue
		}

		normalized := cascade.NormalizeAddress(samples[i].Text)
		vector, err := embedder.Embed(ctx, normalized)
		if err != nil {
			return summary, fmt.Errorf("embed sample %d: %w", i, err)
		}
		if err := store.Upsert(ctx, cache.Entry{
			RawAddress:        samples[i].Text,
			NormalizedAddress: normalized,
			Structured:        address,
			Source:            cache.SourceCRFPipeline,
			Embedding:         vector,
		}); err != nil {
			return summary, fmt.Errorf("upsert sample %d: %w", i, err)
		}
		summary.Cached++
	}
	return summary, nil
}

func cacheable(address structured.Address, minConfidence float64) bool {
	if minConfidence <= 0 {
		return true
	}
	return address.Country != "" &&
		address.Town != "" &&
		address.CountryConfidence >= minConfidence &&
		address.TownConfidence >= minConfidence
}

func reviewReason(address structured.Address, threshold float64) string {
	if threshold <= 0 {
		return ""
	}
	if address.Country == "" {
		return "missing_country"
	}
	if address.Town == "" {
		return "missing_town"
	}
	if address.CountryConfidence < threshold || address.TownConfidence < threshold {
		return "low_confidence"
	}
	return ""
}

func newReviewRow(input string, address structured.Address, reason string) reviewRow {
	return reviewRow{
		Input:             input,
		Country:           address.Country,
		Town:              address.Town,
		CountryConfidence: address.CountryConfidence,
		TownConfidence:    address.TownConfidence,
		Reason:            reason,
	}
}

func writeReview(path string, rows []reviewRow) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return fmt.Errorf("encode review rows: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write review path: %w", err)
	}
	return nil
}

func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
