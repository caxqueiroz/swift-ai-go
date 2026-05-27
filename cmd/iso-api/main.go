package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tipmarket/swift-ai/internal/api"
	"github.com/tipmarket/swift-ai/internal/cache"
	"github.com/tipmarket/swift-ai/internal/cascade"
	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/embedding"
	"github.com/tipmarket/swift-ai/internal/pipeline"
	"github.com/tipmarket/swift-ai/internal/quality"
	isoruntime "github.com/tipmarket/swift-ai/internal/runtime"
)

type options struct {
	addr                      string
	resourcesDir              string
	modelDir                  string
	databaseURL               string
	embeddingAPIKey           string
	embeddingBaseURL          string
	embeddingModel            string
	embeddingDimensions       int
	batchSize                 int
	semanticThreshold         float64
	lexicalThreshold          float64
	highConfidenceThreshold   float64
	mediumConfidenceThreshold float64
	trustSonnetSeed           bool
	trustLLMAssisted          bool
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

	cascadeOpts := []cascade.Option{
		cascade.WithConfig(cascade.Config{
			SemanticThreshold: opts.semanticThreshold,
			LexicalThreshold:  opts.lexicalThreshold,
			SearchLimit:       5,
			TrustSonnetSeed:   opts.trustSonnetSeed,
			TrustLLMAssisted:  opts.trustLLMAssisted,
			QualityThresholds: quality.Thresholds{
				High:   opts.highConfidenceThreshold,
				Medium: opts.mediumConfidenceThreshold,
			},
		}),
	}
	store, embedder, closeCache, err := stage1Dependencies(ctx, opts)
	if err != nil {
		logger.Error("configure stage1", "error", err)
		return 1
	}
	defer closeCache()
	if store != nil && embedder != nil {
		cascadeOpts = append(cascadeOpts, cascade.WithCache(store), cascade.WithEmbedder(embedder))
		logger.Info("stage1 semantic cache enabled")
	} else {
		logger.Warn("stage1 semantic cache disabled; serving through stage2 pipeline")
	}

	converter := cascade.NewService(pipeline.New(cfg, &db, modelRunner), cascadeOpts...)
	mux := http.NewServeMux()
	mux.Handle("/convert", api.NewHandler(converter, logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := &http.Server{
		Addr:              opts.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("starting iso-api", "addr", opts.addr)
	if _, err := fmt.Fprintf(stdout, "iso-api listening on %s\n", opts.addr); err != nil {
		logger.Error("write startup message", "error", err)
		return 1
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		return 1
	}
	return 0
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	cfg := config.Default()
	opts := options{
		addr:                      defaultAddr(),
		resourcesDir:              envDefault("ISO20022_RESOURCES_DIR", cfg.Database.PrefixFolderPath),
		databaseURL:               os.Getenv("DATABASE_URL"),
		embeddingAPIKey:           os.Getenv("OPENAI_API_KEY"),
		embeddingBaseURL:          envDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		embeddingModel:            os.Getenv("EMBEDDING_MODEL"),
		batchSize:                 cfg.BatchSize,
		semanticThreshold:         0.90,
		lexicalThreshold:          0.85,
		highConfidenceThreshold:   quality.DefaultThresholds().High,
		mediumConfidenceThreshold: quality.DefaultThresholds().Medium,
	}
	if value := os.Getenv("EMBEDDING_DIMENSIONS"); value != "" {
		dimensions, err := strconv.Atoi(value)
		if err != nil {
			return options{}, fmt.Errorf("parse EMBEDDING_DIMENSIONS: %w", err)
		}
		opts.embeddingDimensions = dimensions
	}

	fs := flag.NewFlagSet("iso-api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.addr, "addr", opts.addr, "HTTP listen address")
	fs.IntVar(&opts.batchSize, "batch-size", opts.batchSize, "pipeline batch size")
	fs.StringVar(&opts.resourcesDir, "resources-dir", opts.resourcesDir, "resource directory")
	fs.StringVar(&opts.modelDir, "model-dir", "", "model artifact directory")
	fs.StringVar(&opts.databaseURL, "database-url", opts.databaseURL, "Postgres connection string for semantic cache")
	fs.StringVar(&opts.embeddingAPIKey, "embedding-api-key", opts.embeddingAPIKey, "OpenAI-compatible embedding API key")
	fs.StringVar(&opts.embeddingBaseURL, "embedding-base-url", opts.embeddingBaseURL, "OpenAI-compatible base URL")
	fs.StringVar(&opts.embeddingModel, "embedding-model", opts.embeddingModel, "embedding model name")
	fs.IntVar(&opts.embeddingDimensions, "embedding-dimensions", opts.embeddingDimensions, "optional embedding dimensions")
	fs.Float64Var(&opts.semanticThreshold, "semantic-threshold", opts.semanticThreshold, "minimum cosine similarity for Stage 1")
	fs.Float64Var(&opts.lexicalThreshold, "lexical-threshold", opts.lexicalThreshold, "minimum lexical identity for Stage 1")
	fs.Float64Var(&opts.highConfidenceThreshold, "high-confidence-threshold", opts.highConfidenceThreshold, "minimum country/town confidence for resolved status")
	fs.Float64Var(&opts.mediumConfidenceThreshold, "medium-confidence-threshold", opts.mediumConfidenceThreshold, "minimum confidence for medium band")
	fs.BoolVar(&opts.trustSonnetSeed, "trust-sonnet-seed", false, "allow sonnet_seed cache rows to serve directly")
	fs.BoolVar(&opts.trustLLMAssisted, "trust-llm-assisted", false, "allow llm_assisted cache rows to serve directly")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	return opts, nil
}

func stage1Dependencies(ctx context.Context, opts options) (cache.Store, cascade.Embedder, func(), error) {
	if opts.databaseURL == "" || opts.embeddingAPIKey == "" || opts.embeddingModel == "" {
		return nil, nil, func() {}, nil
	}
	store, err := cache.NewPostgresStore(ctx, opts.databaseURL)
	if err != nil {
		return nil, nil, func() {}, err
	}
	embedder, err := embedding.NewOpenAICompatible(embedding.Config{
		APIKey:     opts.embeddingAPIKey,
		BaseURL:    opts.embeddingBaseURL,
		Model:      opts.embeddingModel,
		Dimensions: opts.embeddingDimensions,
	})
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	return store, embedder, store.Close, nil
}

func defaultAddr() string {
	if addr := os.Getenv("ADDR"); addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
