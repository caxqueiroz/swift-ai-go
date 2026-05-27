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
	judgepkg "github.com/tipmarket/swift-ai/internal/judge"
	"github.com/tipmarket/swift-ai/internal/pipeline"
	"github.com/tipmarket/swift-ai/internal/quality"
	"github.com/tipmarket/swift-ai/internal/readers"
	isoruntime "github.com/tipmarket/swift-ai/internal/runtime"
	"github.com/tipmarket/swift-ai/internal/structured"
)

type fillOptions struct {
	inputPath                 string
	resourcesDir              string
	modelDir                  string
	databaseURL               string
	embeddingAPIKey           string
	embeddingBaseURL          string
	embeddingModel            string
	embeddingDimensions       int
	enableLLMJudge            bool
	judgeAPIKey               string
	judgeBaseURL              string
	judgeModel                string
	batchSize                 int
	minCacheConfidence        float64
	reviewThreshold           float64
	highConfidenceThreshold   float64
	mediumConfidenceThreshold float64
	reviewPath                string
	dryRun                    bool
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
	var adjudicator judgepkg.Client
	if !opts.dryRun {
		store, embedder, err = fillDependencies(ctx, opts)
		if err != nil {
			logger.Error("configure cache fill", "error", err)
			return 1
		}
		defer store.Close()
	}
	if opts.enableLLMJudge {
		adjudicator, err = judgeDependency(opts)
		if err != nil {
			logger.Error("configure judge", "error", err)
			return 1
		}
	}

	summary, err := fillCache(ctx, samples, results, store, embedder, adjudicator, opts)
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
		resourcesDir:              envDefault("ISO20022_RESOURCES_DIR", cfg.Database.PrefixFolderPath),
		databaseURL:               os.Getenv("DATABASE_URL"),
		embeddingAPIKey:           os.Getenv("OPENAI_API_KEY"),
		embeddingBaseURL:          envDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		embeddingModel:            os.Getenv("EMBEDDING_MODEL"),
		judgeAPIKey:               envDefault("JUDGE_API_KEY", os.Getenv("OPENAI_API_KEY")),
		judgeBaseURL:              envDefault("JUDGE_BASE_URL", envDefault("OPENAI_BASE_URL", "https://api.openai.com/v1")),
		judgeModel:                os.Getenv("JUDGE_MODEL"),
		batchSize:                 cfg.BatchSize,
		minCacheConfidence:        0,
		reviewThreshold:           0.85,
		highConfidenceThreshold:   quality.DefaultThresholds().High,
		mediumConfidenceThreshold: quality.DefaultThresholds().Medium,
		embeddingDimensions:       0,
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
	fs.BoolVar(&opts.enableLLMJudge, "enable-llm-judge", opts.enableLLMJudge, "use constrained LLM judge for non-high-confidence rows")
	fs.StringVar(&opts.judgeAPIKey, "judge-api-key", opts.judgeAPIKey, "OpenAI-compatible judge API key")
	fs.StringVar(&opts.judgeBaseURL, "judge-base-url", opts.judgeBaseURL, "OpenAI-compatible judge base URL")
	fs.StringVar(&opts.judgeModel, "judge-model", opts.judgeModel, "LLM judge model name")
	fs.Float64Var(&opts.minCacheConfidence, "min-cache-confidence", opts.minCacheConfidence, "minimum country/town confidence to cache")
	fs.Float64Var(&opts.reviewThreshold, "review-threshold", opts.reviewThreshold, "confidence threshold for review export")
	fs.Float64Var(&opts.highConfidenceThreshold, "high-confidence-threshold", opts.highConfidenceThreshold, "minimum country/town confidence to cache Swift output directly")
	fs.Float64Var(&opts.mediumConfidenceThreshold, "medium-confidence-threshold", opts.mediumConfidenceThreshold, "minimum country/town confidence for medium-band review")
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
	if opts.enableLLMJudge {
		if opts.judgeAPIKey == "" {
			return fillOptions{}, errors.New("judge API key is required when --enable-llm-judge is set")
		}
		if opts.judgeModel == "" {
			return fillOptions{}, errors.New("judge model is required when --enable-llm-judge is set")
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

func judgeDependency(opts fillOptions) (judgepkg.Client, error) {
	return judgepkg.NewOpenAICompatible(judgepkg.Config{
		APIKey:  opts.judgeAPIKey,
		BaseURL: opts.judgeBaseURL,
		Model:   opts.judgeModel,
	})
}

type cacheWriter interface {
	Upsert(ctx context.Context, entry cache.Entry) error
}

type cacheEmbedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
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

func fillCache(ctx context.Context, samples []core.AddressSample, results []core.Result, store cacheWriter, embedder cacheEmbedder, adjudicator judgepkg.Client, opts fillOptions) (fillSummary, error) {
	if len(samples) != len(results) {
		return fillSummary{}, fmt.Errorf("sample/result length mismatch: %d samples, %d results", len(samples), len(results))
	}

	summary := fillSummary{Processed: len(samples)}
	for i, result := range results {
		address := structured.FromResult(result)
		assessment := quality.Assess(address, quality.Thresholds{
			High:   opts.highConfidenceThreshold,
			Medium: opts.mediumConfidenceThreshold,
		})

		if assessment.Status == quality.StatusResolved && assessment.Band == quality.BandHigh {
			cached, err := writeCacheEntry(ctx, samples[i].Text, address, cache.SourceCRFPipeline, store, embedder, opts)
			if err != nil {
				return summary, fmt.Errorf("cache high-confidence sample %d: %w", i, err)
			}
			if cached {
				summary.Cached++
			}
			continue
		}

		if adjudicator != nil {
			request := newJudgeRequest(samples[i].Text, result)
			decision, err := adjudicator.Judge(ctx, request)
			if err != nil {
				return summary, fmt.Errorf("judge sample %d: %w", i, err)
			}
			if valid, ok := judgepkg.ValidateDecision(request, decision); ok {
				judged := applyDecision(address, request, valid)
				cached, err := writeCacheEntry(ctx, samples[i].Text, judged, cache.SourceLLMAssisted, store, embedder, opts)
				if err != nil {
					return summary, fmt.Errorf("cache judged sample %d: %w", i, err)
				}
				if cached {
					summary.Cached++
				}
				continue
			}
			summary.ReviewRows = append(summary.ReviewRows, newReviewRow(samples[i].Text, address, "judge_unresolved"))
			summary.Skipped++
			continue
		}

		reason := reviewReason(address, opts.reviewThreshold)
		if reason == "" {
			reason = string(assessment.Reason)
		}
		summary.ReviewRows = append(summary.ReviewRows, newReviewRow(samples[i].Text, address, reason))
		summary.Skipped++
	}
	return summary, nil
}

func writeCacheEntry(ctx context.Context, input string, address structured.Address, source cache.Source, store cacheWriter, embedder cacheEmbedder, opts fillOptions) (bool, error) {
	if !cacheable(address, opts.minCacheConfidence) {
		return false, nil
	}
	if opts.dryRun {
		return false, nil
	}
	if store == nil || embedder == nil {
		return false, errors.New("cache writer and embedder are required")
	}

	normalized := cascade.NormalizeAddress(input)
	vector, err := embedder.Embed(ctx, normalized)
	if err != nil {
		return false, fmt.Errorf("embed address: %w", err)
	}
	if err := store.Upsert(ctx, cache.Entry{
		RawAddress:        input,
		NormalizedAddress: normalized,
		Structured:        address,
		Source:            source,
		Embedding:         vector,
	}); err != nil {
		return false, fmt.Errorf("upsert address cache: %w", err)
	}
	return true, nil
}

func newJudgeRequest(input string, result core.Result) judgepkg.Request {
	request := judgepkg.Request{
		Input: input,
	}
	for _, match := range result.FuzzyResult.CountryMatches {
		if match.Origin == "" || match.Origin == "NO COUNTRY" {
			continue
		}
		request.Countries = append(request.Countries, judgepkg.CountryCandidate{
			Code:        match.Origin,
			Score:       match.FinalScore,
			Matched:     match.Matched,
			Possibility: match.Possibility,
		})
		if len(request.Countries) == 5 {
			break
		}
	}
	for _, match := range result.FuzzyResult.TownMatches {
		if match.Possibility == "" || match.Possibility == "NO TOWN" || match.Origin == "NO TOWN" {
			continue
		}
		request.Towns = append(request.Towns, judgepkg.TownCandidate{
			Name:        match.Possibility,
			CountryCode: match.Origin,
			Score:       match.FinalScore,
			Matched:     match.Matched,
		})
		if len(request.Towns) == 5 {
			break
		}
	}
	return request
}

func applyDecision(address structured.Address, request judgepkg.Request, decision judgepkg.Decision) structured.Address {
	address.Country = decision.Country
	address.Town = decision.Town
	for _, candidate := range request.Countries {
		if strings.EqualFold(candidate.Code, decision.Country) {
			address.CountryConfidence = candidate.Score
			break
		}
	}
	for _, candidate := range request.Towns {
		if strings.EqualFold(candidate.Name, decision.Town) && strings.EqualFold(candidate.CountryCode, decision.Country) {
			address.TownConfidence = candidate.Score
			break
		}
	}
	return address
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
