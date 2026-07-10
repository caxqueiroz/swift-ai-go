package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/output"
	"github.com/tipmarket/swift-ai/internal/pipeline"
	"github.com/tipmarket/swift-ai/internal/postaldb"
	"github.com/tipmarket/swift-ai/internal/readers"
	isoruntime "github.com/tipmarket/swift-ai/internal/runtime"
)

const version = "iso-run dev"

type cliOptions struct {
	inputPath                   string
	outputPath                  string
	batchSize                   int
	resourcesDir                string
	modelDir                    string
	postalDBPath                string
	delimiter                   string
	addressColumn               string
	idColumn                    string
	suggestedCountryColumn      string
	forceSuggestedCountryColumn string
	verbose                     bool
	version                     bool
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
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			logger.Error("write version", "error", err)
			return 1
		}
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

	readSamples, err := selectReaderWithOptions(opts.inputPath, opts)
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

	pipelineOpts, closePipelineOpts, err := pipelineOptions(opts)
	if err != nil {
		logger.Error("configure pipeline", "error", err)
		return 1
	}
	defer closePipelineOpts()

	results, err := pipeline.New(cfg, &db, modelRunner, pipelineOpts...).Run(ctx, samples)
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
		batchSize:                   cfg.BatchSize,
		resourcesDir:                cfg.Database.PrefixFolderPath,
		postalDBPath:                os.Getenv("ISO20022_POSTAL_DB_PATH"),
		addressColumn:               readers.DefaultAddressColumn,
		idColumn:                    readers.DefaultIDColumn,
		suggestedCountryColumn:      readers.DefaultSuggestedCountryColumn,
		forceSuggestedCountryColumn: readers.DefaultForceSuggestedCountryColumn,
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
	fs.StringVar(&opts.postalDBPath, "postal-db", opts.postalDBPath, "optional combined postal location SQLite database path")
	fs.StringVar(&opts.delimiter, "delimiter", "", "input delimiter override for CSV/TSV: comma, tab, semicolon, pipe, or a single character")
	fs.StringVar(&opts.addressColumn, "address-column", opts.addressColumn, "input column containing the unstructured address")
	fs.StringVar(&opts.idColumn, "id-column", opts.idColumn, "optional input column containing a stable row ID")
	fs.StringVar(&opts.suggestedCountryColumn, "suggested-country-column", opts.suggestedCountryColumn, "optional input column containing an ISO country hint")
	fs.StringVar(&opts.forceSuggestedCountryColumn, "force-suggested-country-column", opts.forceSuggestedCountryColumn, "optional input column forcing the ISO country hint")
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

func pipelineOptions(opts cliOptions) ([]pipeline.Option, func(), error) {
	if opts.postalDBPath == "" {
		return nil, func() {}, nil
	}
	runner, err := postaldb.Open(opts.postalDBPath)
	if err != nil {
		return nil, nil, err
	}
	return []pipeline.Option{pipeline.WithPostcodeRunner(runner)}, func() {
		_ = runner.Close()
	}, nil
}

func selectReaderWithOptions(path string, opts cliOptions) (func() ([]core.AddressSample, error), error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return func() ([]core.AddressSample, error) {
			return readers.ReadText(path)
		}, nil
	case ".csv":
		comma, err := delimiterForPath(path, opts.delimiter)
		if err != nil {
			return nil, err
		}
		return func() ([]core.AddressSample, error) {
			return readers.ReadDelimitedWithOptions(path, comma, readerOptions(opts))
		}, nil
	case ".tsv":
		comma, err := delimiterForPath(path, opts.delimiter)
		if err != nil {
			return nil, err
		}
		return func() ([]core.AddressSample, error) {
			return readers.ReadDelimitedWithOptions(path, comma, readerOptions(opts))
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input format %q", filepath.Ext(path))
	}
}

func readerOptions(opts cliOptions) readers.DelimitedOptions {
	return readers.DelimitedOptions{
		AddressColumn:               opts.addressColumn,
		IDColumn:                    opts.idColumn,
		SuggestedCountryColumn:      opts.suggestedCountryColumn,
		ForceSuggestedCountryColumn: opts.forceSuggestedCountryColumn,
	}
}

func delimiterForPath(path, override string) (rune, error) {
	if override != "" {
		return parseDelimiter(override)
	}
	if strings.EqualFold(filepath.Ext(path), ".tsv") {
		return '\t', nil
	}
	return ',', nil
}

func parseDelimiter(value string) (rune, error) {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "comma", ",":
		return ',', nil
	case "tab", "\\t":
		return '\t', nil
	case "semicolon", ";":
		return ';', nil
	case "pipe", "|":
		return '|', nil
	default:
		runes := []rune(trimmed)
		if len(runes) == 1 {
			return runes[0], nil
		}
		return 0, fmt.Errorf("unsupported delimiter %q", value)
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
