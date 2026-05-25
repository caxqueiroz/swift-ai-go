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

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/model"
	"github.com/tipmarket/swift-ai/internal/output"
	"github.com/tipmarket/swift-ai/internal/pipeline"
	"github.com/tipmarket/swift-ai/internal/readers"
	"github.com/tipmarket/swift-ai/internal/resources"
)

const version = "iso-run dev"

type cliOptions struct {
	inputPath    string
	outputPath   string
	batchSize    int
	resourcesDir string
	modelDir     string
	verbose      bool
	version      bool
}

type outputFormat int

const (
	outputHumanReadable outputFormat = iota
	outputJSON
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	opts, err := parseArgs(args, stderr)
	if err != nil {
		logger.Error("parse flags", "error", err)
		return 2
	}
	if opts.version {
		fmt.Fprintln(stdout, version)
		return 0
	}

	cfg := config.Default()
	cfg.BatchSize = opts.batchSize
	cfg.Database.PrefixFolderPath = opts.resourcesDir
	if opts.modelDir != "" {
		cfg.CRF.ModelPath = filepath.Join(opts.modelDir, "address_transformer.onnx")
		cfg.CRF.ModelConfigPath = filepath.Join(opts.modelDir, "address_transformer.config.json")
		cfg.CRF.CRFConfigPath = filepath.Join(opts.modelDir, "address_crf.json")
	}

	readSamples, err := selectReader(opts.inputPath)
	if err != nil {
		logger.Error("read input", "error", err)
		return 1
	}
	format, err := selectOutputFormat(opts.outputPath)
	if err != nil {
		logger.Error("write output", "error", err)
		return 1
	}
	samples, err := readSamples()
	if err != nil {
		logger.Error("read input", "error", err)
		return 1
	}

	db, err := loadDatabase(cfg)
	if err != nil {
		logger.Error("load resources", "error", err)
		return 1
	}

	modelRunner, engine, err := loadModelRunner(cfg)
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

	if err := writeOutput(format, opts.outputPath, results, cfg, opts.verbose); err != nil {
		logger.Error("write output", "error", err)
		return 1
	}

	return 0
}

func parseArgs(args []string, stderr io.Writer) (cliOptions, error) {
	cfg := config.Default()
	opts := cliOptions{
		batchSize:    cfg.BatchSize,
		resourcesDir: cfg.Database.PrefixFolderPath,
	}

	fs := flag.NewFlagSet("iso-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.inputPath, "input-path", "", "input file path")
	fs.StringVar(&opts.inputPath, "i", "", "input file path")
	fs.StringVar(&opts.outputPath, "output-path", "", "output file path")
	fs.StringVar(&opts.outputPath, "o", "", "output file path")
	fs.IntVar(&opts.batchSize, "batch-size", opts.batchSize, "pipeline batch size")
	fs.StringVar(&opts.resourcesDir, "resources-dir", opts.resourcesDir, "resource directory")
	fs.StringVar(&opts.modelDir, "model-dir", "", "model artifact directory")
	fs.BoolVar(&opts.verbose, "verbose", false, "include verbose output")
	fs.BoolVar(&opts.verbose, "v", false, "include verbose output")
	fs.BoolVar(&opts.version, "version", false, "print version")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if opts.version {
		return opts, nil
	}
	if opts.inputPath == "" {
		return cliOptions{}, errors.New("input path is required")
	}
	if opts.outputPath == "" {
		return cliOptions{}, errors.New("output path is required")
	}
	return opts, nil
}

func selectReader(path string) (func() ([]core.AddressSample, error), error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return func() ([]core.AddressSample, error) {
			return readers.ReadText(path)
		}, nil
	case ".csv":
		return func() ([]core.AddressSample, error) {
			return readers.ReadDelimited(path, ',', readers.DefaultAddressColumn)
		}, nil
	case ".tsv":
		return func() ([]core.AddressSample, error) {
			return readers.ReadDelimited(path, '\t', readers.DefaultAddressColumn)
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input format %q", filepath.Ext(path))
	}
}

func selectOutputFormat(path string) (outputFormat, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv":
		return outputHumanReadable, nil
	case ".json":
		return outputJSON, nil
	default:
		return outputHumanReadable, fmt.Errorf("unsupported output format %q", filepath.Ext(path))
	}
}

func writeOutput(format outputFormat, path string, results []core.Result, cfg config.Config, verbose bool) error {
	switch format {
	case outputHumanReadable:
		return output.WriteHumanReadable(path, results, cfg.PostProcessing.ShowInferredCountry, verbose)
	case outputJSON:
		return output.WriteJSON(path, results, verbose)
	default:
		return fmt.Errorf("unsupported output format %d", format)
	}
}

func loadDatabase(cfg config.Config) (resources.Database, error) {
	dbPath := func(path string) string {
		return resourcePath(cfg.Database.PrefixFolderPath, path)
	}

	countryAlpha2, countryPossibilities, err := resources.LoadCountryAliases(
		dbPath(cfg.Database.CountryAliases),
		dbPath(cfg.Database.CountryProvinceAliases),
	)
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country aliases: %w", err)
	}

	townAliases, err := resources.LoadCompressedJSON[map[string][]string](dbPath(cfg.Database.TownAliases))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load town aliases: %w", err)
	}

	townPossibilities, townPopulations, largestTownCountry, err := resources.LoadTownsFromParquet(dbPath(cfg.Database.GeonamesParquet), townAliases)
	if err != nil {
		return resources.Database{}, fmt.Errorf("load towns: %w", err)
	}

	countryTownSameName, err := resources.LoadJSON[map[string]string](dbPath(cfg.Database.CountryTownSameName))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country-town same-name data: %w", err)
	}
	countryGroupings, err := resources.LoadJSON[map[string][]string](dbPath(cfg.Database.CountryGroupings))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country groupings: %w", err)
	}
	countrySpecs, err := resources.LoadCompressedJSON[map[string]resources.CountrySpec](dbPath(cfg.Database.CountrySpecs))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country specs: %w", err)
	}
	provinces, err := resources.LoadCompressedJSON[map[string][]string](dbPath(cfg.Database.CountryProvinceAliases))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load provinces: %w", err)
	}

	return resources.Database{
		CountryAlpha2:        countryAlpha2,
		CountryPossibilities: countryPossibilities,
		TownPossibilities:    townPossibilities,
		TownPopulations:      townPopulations,
		LargestTownCountry:   largestTownCountry,
		CountryTownSameName:  countryTownSameName,
		CountryGroupings:     countryGroupings,
		CountrySpecs:         countrySpecs,
		Provinces:            provinces,
	}, nil
}

func resourcePath(prefix string, path string) string {
	if filepath.IsAbs(path) || prefix == "" {
		return path
	}
	return filepath.Join(prefix, path)
}

type convertedModelConfig struct {
	Vocabulary         []string        `json:"vocabulary"`
	BIOTagsToKeep      json.RawMessage `json:"bio_tags_to_keep"`
	TagsToKeep         json.RawMessage `json:"tags_to_keep"`
	IDToCountry        map[int]string  `json:"id_to_country"`
	StrictBeforeInside bool            `json:"strict_before_inside"`
	InputNames         []string        `json:"input_names"`
	OutputNames        []string        `json:"output_names"`
	TagCount           int             `json:"tag_count"`
	MaxSequenceLength  int             `json:"max_sequence_length"`
}

type convertedCRFConfig struct {
	Start             []float64   `json:"start"`
	End               []float64   `json:"end"`
	StartTransitions  []float64   `json:"start_transitions"`
	EndTransitions    []float64   `json:"end_transitions"`
	Transitions       [][]float64 `json:"transitions"`
	TransitionsOrder2 [][]float64 `json:"transitions_order_2"`
}

func loadModelRunner(cfg config.Config) (*model.Runner, *model.ONNXEngine, error) {
	modelCfg, err := loadModelConfig(cfg.CRF.ModelConfigPath)
	if err != nil {
		return nil, nil, err
	}
	bioTags, err := decodeBIOTags(modelCfg.BIOTagsToKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("decode bio_tags_to_keep: %w", err)
	}
	tags, err := decodeTags(modelCfg.TagsToKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("decode tags_to_keep: %w", err)
	}

	crfCfg, err := loadCRFConfig(cfg.CRF.CRFConfigPath)
	if err != nil {
		return nil, nil, err
	}
	tokenizer, err := model.NewCharacterTokenizer(modelCfg.Vocabulary)
	if err != nil {
		return nil, nil, fmt.Errorf("create tokenizer: %w", err)
	}

	maxSequenceLength := cfg.CRF.MaxSequenceLength
	if modelCfg.MaxSequenceLength > 0 {
		maxSequenceLength = modelCfg.MaxSequenceLength
	}
	tagCount := modelCfg.TagCount
	if tagCount == 0 {
		tagCount = len(bioTags)
	}

	engine, err := model.NewONNXInferenceEngine(model.ONNXConfig{
		ModelPath:         cfg.CRF.ModelPath,
		SharedLibraryPath: os.Getenv("ISO20022_ONNX_RUNTIME"),
		InputNames:        modelCfg.InputNames,
		OutputNames:       modelCfg.OutputNames,
		TagCount:          tagCount,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ONNX inference engine: %w", err)
	}

	runner, err := model.NewRunner(tokenizer, engine, crfCfg, model.RunnerConfig{
		MaxSequenceLength:  maxSequenceLength,
		BIOTagsToKeep:      bioTags,
		TagsToKeep:         tags,
		IDToCountry:        modelCfg.IDToCountry,
		StrictBeforeInside: modelCfg.StrictBeforeInside,
	})
	if err != nil {
		_ = engine.Close()
		return nil, nil, fmt.Errorf("create model runner: %w", err)
	}

	return runner, engine, nil
}

func loadModelConfig(path string) (convertedModelConfig, error) {
	var cfg convertedModelConfig
	if err := loadJSON(path, &cfg); err != nil {
		return convertedModelConfig{}, fmt.Errorf("load model config %q: %w", path, err)
	}
	return cfg, nil
}

func loadCRFConfig(path string) (model.CRF, error) {
	var cfg convertedCRFConfig
	if err := loadJSON(path, &cfg); err != nil {
		return model.CRF{}, fmt.Errorf("load CRF config %q: %w", path, err)
	}
	start := cfg.Start
	if len(start) == 0 {
		start = cfg.StartTransitions
	}
	end := cfg.End
	if len(end) == 0 {
		end = cfg.EndTransitions
	}
	return model.CRF{
		Start:             start,
		End:               end,
		Transitions:       cfg.Transitions,
		TransitionsOrder2: cfg.TransitionsOrder2,
	}, nil
}

func loadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	return nil
}

func decodeBIOTags(raw json.RawMessage) ([]core.BIOTag, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing bio_tags_to_keep")
	}

	var tags []core.BIOTag
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags, nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	tags = make([]core.BIOTag, len(names))
	for i, name := range names {
		tag, err := parseBIOTag(name)
		if err != nil {
			return nil, fmt.Errorf("tag %d: %w", i, err)
		}
		tags[i] = tag
	}
	return tags, nil
}

func decodeTags(raw json.RawMessage) ([]core.Tag, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing tags_to_keep")
	}

	var tags []core.Tag
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags, nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	tags = make([]core.Tag, len(names))
	for i, name := range names {
		tags[i] = core.Tag(name)
	}
	return tags, nil
}

func parseBIOTag(name string) (core.BIOTag, error) {
	name = strings.TrimSpace(name)
	if name == core.BioOther.String() {
		return core.BIOTag{BIO: core.BioOther, Tag: core.TagOther}, nil
	}
	for _, bio := range []core.BIO{core.BioBefore, core.BioInside} {
		if strings.HasPrefix(name, bio.String()) {
			tagName := strings.TrimPrefix(name, bio.String())
			if tagName == "" {
				return core.BIOTag{}, fmt.Errorf("empty tag in %q", name)
			}
			return core.BIOTag{BIO: bio, Tag: core.Tag(tagName)}, nil
		}
	}
	if _, err := strconv.Atoi(name); err == nil {
		return core.BIOTag{}, fmt.Errorf("numeric BIO tag %q is unsupported", name)
	}
	return core.BIOTag{}, fmt.Errorf("unsupported BIO tag %q", name)
}
