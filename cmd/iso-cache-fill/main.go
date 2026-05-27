package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	maxRecords                int
	countryFilter             string
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

	source, err := openSampleSource(opts.inputPath, opts)
	if err != nil {
		logger.Error("open input", "error", err)
		return 1
	}
	defer func() {
		if err := source.Close(); err != nil {
			logger.Error("close input", "error", err)
		}
	}()

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

	var reviews *reviewFileWriter
	if opts.reviewPath != "" {
		reviews, err = newReviewFileWriter(opts.reviewPath)
		if err != nil {
			logger.Error("open review rows", "error", err)
			return 1
		}
	}

	summary, err := fillFromSource(ctx, source, pipeline.New(cfg, &db, modelRunner), store, embedder, adjudicator, reviews, opts)
	if err != nil {
		if reviews != nil {
			if closeErr := reviews.Close(); closeErr != nil {
				logger.Error("close review rows", "error", closeErr)
			}
		}
		logger.Error("fill cache", "error", err)
		return 1
	}
	if reviews != nil {
		if err := reviews.Close(); err != nil {
			logger.Error("close review rows", "error", err)
			return 1
		}
	}

	if _, err := fmt.Fprintf(stdout, "processed=%d cached=%d review=%d skipped=%d\n",
		summary.Processed, summary.Cached, summary.ReviewCount, summary.Skipped); err != nil {
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
	fs.IntVar(&opts.maxRecords, "max-records", 0, "maximum input records to process; 0 means no limit")
	fs.StringVar(&opts.countryFilter, "country", "", "optional ISO alpha-2 country folder to process when input path is a directory")
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

func fillFromSource(ctx context.Context, source sampleSource, runner *pipeline.Pipeline, store cacheWriter, embedder cacheEmbedder, adjudicator judgepkg.Client, reviews reviewSink, opts fillOptions) (fillSummary, error) {
	var summary fillSummary
	batchSize := opts.batchSize
	if batchSize <= 0 {
		batchSize = 1024
	}

	for {
		samples, err := source.NextBatch(batchSize)
		if err != nil {
			return summary, err
		}
		if len(samples) == 0 {
			break
		}

		results, err := runner.Run(ctx, samples)
		if err != nil {
			return summary, fmt.Errorf("run pipeline after %d processed samples: %w", summary.Processed, err)
		}

		batchSummary, err := fillCacheWithReviews(ctx, samples, results, store, embedder, adjudicator, reviews, opts)
		if err != nil {
			return summary, err
		}
		mergeSummary(&summary, batchSummary)
	}
	return summary, nil
}

func mergeSummary(dst *fillSummary, src fillSummary) {
	dst.Processed += src.Processed
	dst.Cached += src.Cached
	dst.Skipped += src.Skipped
	dst.ReviewCount += src.ReviewCount
	dst.ReviewRows = append(dst.ReviewRows, src.ReviewRows...)
}

type sampleSource interface {
	NextBatch(batchSize int) ([]core.AddressSample, error)
	Close() error
}

func openSampleSource(path string, opts fillOptions) (sampleSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat input path %q: %w", path, err)
	}
	if info.IsDir() {
		files, err := geoJSONInputFiles(path, opts.countryFilter)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("no OpenAddresses address GeoJSON files found under %q", path)
		}
		return &geoJSONSource{files: files, maxRecords: opts.maxRecords}, nil
	}
	if strings.EqualFold(filepath.Ext(path), ".geojson") {
		return &geoJSONSource{
			files:      []geoJSONInputFile{{Path: path, CountryCode: countryCodeForGeoJSON(filepath.Dir(path), path)}},
			maxRecords: opts.maxRecords,
		}, nil
	}

	samples, err := readSamples(path)
	if err != nil {
		return nil, err
	}
	if opts.maxRecords > 0 && len(samples) > opts.maxRecords {
		samples = samples[:opts.maxRecords]
	}
	return &memorySampleSource{samples: samples}, nil
}

type memorySampleSource struct {
	samples []core.AddressSample
	offset  int
}

func (s *memorySampleSource) NextBatch(batchSize int) ([]core.AddressSample, error) {
	if s.offset >= len(s.samples) {
		return nil, nil
	}
	if batchSize <= 0 {
		batchSize = len(s.samples)
	}
	end := min(s.offset+batchSize, len(s.samples))
	batch := s.samples[s.offset:end]
	s.offset = end
	return batch, nil
}

func (s *memorySampleSource) Close() error {
	return nil
}

type geoJSONInputFile struct {
	Path        string
	CountryCode string
}

type geoJSONSource struct {
	files       []geoJSONInputFile
	fileIndex   int
	current     *os.File
	currentInfo geoJSONInputFile
	scanner     *bufio.Scanner
	records     int
	maxRecords  int
}

func (s *geoJSONSource) NextBatch(batchSize int) ([]core.AddressSample, error) {
	if batchSize <= 0 {
		batchSize = 1024
	}
	batch := make([]core.AddressSample, 0, batchSize)
	for len(batch) < batchSize {
		if s.maxRecords > 0 && s.records >= s.maxRecords {
			return batch, nil
		}
		if s.scanner == nil {
			ok, err := s.openNext()
			if err != nil {
				return nil, err
			}
			if !ok {
				return batch, nil
			}
		}

		for s.scanner.Scan() {
			line := bytes.TrimSpace(s.scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			sample, ok, err := readers.ParseOpenAddressesFeature(line, s.currentInfo.CountryCode)
			if err != nil {
				return nil, fmt.Errorf("parse %q: %w", s.currentInfo.Path, err)
			}
			if !ok {
				continue
			}
			batch = append(batch, sample)
			s.records++
			if len(batch) == batchSize || (s.maxRecords > 0 && s.records >= s.maxRecords) {
				return batch, nil
			}
		}
		if err := s.scanner.Err(); err != nil {
			return nil, fmt.Errorf("scan %q: %w", s.currentInfo.Path, err)
		}
		if err := s.closeCurrent(); err != nil {
			return nil, err
		}
	}
	return batch, nil
}

func (s *geoJSONSource) Close() error {
	return s.closeCurrent()
}

func (s *geoJSONSource) openNext() (bool, error) {
	if s.fileIndex >= len(s.files) {
		return false, nil
	}
	info := s.files[s.fileIndex]
	s.fileIndex++

	file, err := os.Open(info.Path)
	if err != nil {
		return false, fmt.Errorf("open OpenAddresses GeoJSON file %q: %w", info.Path, err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxOpenAddressesLineBytes)

	s.current = file
	s.currentInfo = info
	s.scanner = scanner
	return true, nil
}

func (s *geoJSONSource) closeCurrent() error {
	if s.current == nil {
		s.scanner = nil
		return nil
	}
	err := s.current.Close()
	s.current = nil
	s.currentInfo = geoJSONInputFile{}
	s.scanner = nil
	if err != nil {
		return fmt.Errorf("close OpenAddresses GeoJSON file: %w", err)
	}
	return nil
}

const maxOpenAddressesLineBytes = 10 * 1024 * 1024

func geoJSONInputFiles(root string, countryFilter string) ([]geoJSONInputFile, error) {
	countryFilter = strings.ToUpper(strings.TrimSpace(countryFilter))
	var files []geoJSONInputFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isAddressGeoJSONFile(entry.Name()) {
			return nil
		}
		countryCode := countryCodeForGeoJSON(root, path)
		if countryFilter != "" && countryCode != countryFilter {
			return nil
		}
		files = append(files, geoJSONInputFile{Path: path, CountryCode: countryCode})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk OpenAddresses directory %q: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func isAddressGeoJSONFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".geojson") && strings.Contains(lower, "addresses")
}

func countryCodeForGeoJSON(root string, path string) string {
	parent := filepath.Base(filepath.Dir(path))
	if len(parent) == 2 {
		return strings.ToUpper(parent)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	first := rel
	if index := strings.IndexRune(rel, filepath.Separator); index >= 0 {
		first = rel[:index]
	}
	if len(first) == 2 {
		return strings.ToUpper(first)
	}
	return ""
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
	Processed   int
	Cached      int
	Skipped     int
	ReviewCount int
	ReviewRows  []reviewRow
}

type reviewRow struct {
	Input             string  `json:"input"`
	Country           string  `json:"country"`
	Town              string  `json:"town"`
	CountryConfidence float64 `json:"country_confidence"`
	TownConfidence    float64 `json:"town_confidence"`
	Reason            string  `json:"reason"`
}

type reviewSink interface {
	Add(row reviewRow) error
}

type reviewFileWriter struct {
	file  *os.File
	count int
}

func newReviewFileWriter(path string) (*reviewFileWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create review path %q: %w", path, err)
	}
	if _, err := io.WriteString(file, "["); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("start review JSON: %w", err)
	}
	return &reviewFileWriter{file: file}, nil
}

func (w *reviewFileWriter) Add(row reviewRow) error {
	if w == nil || w.file == nil {
		return errors.New("review writer is closed")
	}
	if w.count == 0 {
		if _, err := io.WriteString(w.file, "\n"); err != nil {
			return fmt.Errorf("write review separator: %w", err)
		}
	} else if _, err := io.WriteString(w.file, ",\n"); err != nil {
		return fmt.Errorf("write review separator: %w", err)
	}
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode review row: %w", err)
	}
	if _, err := io.WriteString(w.file, "  "); err != nil {
		return fmt.Errorf("write review indent: %w", err)
	}
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("write review row: %w", err)
	}
	w.count++
	return nil
}

func (w *reviewFileWriter) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	suffix := "]\n"
	if w.count > 0 {
		suffix = "\n]\n"
	}
	_, writeErr := io.WriteString(w.file, suffix)
	closeErr := w.file.Close()
	w.file = nil
	if writeErr != nil {
		return fmt.Errorf("finish review JSON: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close review file: %w", closeErr)
	}
	return nil
}

func fillCache(ctx context.Context, samples []core.AddressSample, results []core.Result, store cacheWriter, embedder cacheEmbedder, adjudicator judgepkg.Client, opts fillOptions) (fillSummary, error) {
	return fillCacheWithReviews(ctx, samples, results, store, embedder, adjudicator, nil, opts)
}

func fillCacheWithReviews(ctx context.Context, samples []core.AddressSample, results []core.Result, store cacheWriter, embedder cacheEmbedder, adjudicator judgepkg.Client, reviews reviewSink, opts fillOptions) (fillSummary, error) {
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
			if err := addReviewRow(&summary, newReviewRow(samples[i].Text, address, "judge_unresolved"), reviews, opts); err != nil {
				return summary, err
			}
			continue
		}

		reason := reviewReason(address, opts.reviewThreshold)
		if reason == "" {
			reason = string(assessment.Reason)
		}
		if err := addReviewRow(&summary, newReviewRow(samples[i].Text, address, reason), reviews, opts); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func addReviewRow(summary *fillSummary, row reviewRow, reviews reviewSink, opts fillOptions) error {
	summary.Skipped++
	summary.ReviewCount++
	if reviews != nil {
		return reviews.Add(row)
	}
	if opts.reviewPath != "" {
		summary.ReviewRows = append(summary.ReviewRows, row)
	}
	return nil
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

func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
