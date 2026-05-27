package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"
)

const defaultSQLitePath = "/Volumes/cax-t7/Data/DataAddr/addresses.sqlite"

type options struct {
	sqlitePath       string
	apiURL           string
	country          string
	limit            int
	seed             uint64
	batchSize        int
	timeout          time.Duration
	mismatchesPath   string
	includeBlankTown bool
	inputMode        string
}

type commandReport struct {
	SQLite           string       `json:"sqlite"`
	APIURL           string       `json:"api_url"`
	Country          string       `json:"country,omitempty"`
	RequestedLimit   int          `json:"requested_limit"`
	Sampled          int          `json:"sampled"`
	Seed             uint64       `json:"seed"`
	BatchSize        int          `json:"batch_size"`
	Mismatches       int          `json:"mismatches"`
	MismatchesPath   string       `json:"mismatches_path,omitempty"`
	IncludeBlankTown bool         `json:"include_blank_town"`
	InputMode        string       `json:"input_mode"`
	Metrics          metricReport `json:"metrics"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, logger))
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, logger *slog.Logger) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	db, err := openSQLite(opts.sqlitePath)
	if err != nil {
		logger.Error("open sqlite", "error", err)
		return 1
	}
	defer func() {
		_ = db.Close()
	}()

	groups, err := loadTownGroups(ctx, db, opts.country, opts.includeBlankTown)
	if err != nil {
		logger.Error("load town groups", "error", err)
		return 1
	}
	if len(groups) == 0 {
		logger.Error("no town groups found", "country", opts.country)
		return 1
	}

	rng := rand.New(rand.NewPCG(opts.seed, opts.seed^0x9e3779b97f4a7c15))
	plans := planSamples(groups, opts.limit, rng)
	records, err := loadSampledAddresses(ctx, db, plans)
	if err != nil {
		logger.Error("load sampled addresses", "error", err)
		return 1
	}

	mismatches, closeMismatches, err := newMismatchWriter(opts.mismatchesPath)
	if err != nil {
		logger.Error("open mismatches", "error", err)
		return 1
	}
	defer func() {
		if err := closeMismatches(); err != nil {
			logger.Error("close mismatches", "error", err)
		}
	}()

	client := &http.Client{Timeout: opts.timeout}
	recorder := newMetricRecorder()
	mismatchCount := 0
	for start := 0; start < len(records); start += opts.batchSize {
		end := min(start+opts.batchSize, len(records))
		rows, err := convertBatch(ctx, client, opts.apiURL, records[start:end], opts.inputMode)
		if err != nil {
			logger.Error("convert batch", "start", start, "end", end, "error", err)
			return 1
		}
		for _, row := range rows {
			recorder.Add(row)
			if isMismatch(row) {
				mismatchCount++
				if err := mismatches.Write(row); err != nil {
					logger.Error("write mismatch", "error", err)
					return 1
				}
			}
		}
	}

	report := commandReport{
		SQLite:           opts.sqlitePath,
		APIURL:           opts.apiURL,
		Country:          canonicalCountry(opts.country),
		RequestedLimit:   opts.limit,
		Sampled:          len(records),
		Seed:             opts.seed,
		BatchSize:        opts.batchSize,
		Mismatches:       mismatchCount,
		MismatchesPath:   opts.mismatchesPath,
		IncludeBlankTown: opts.includeBlankTown,
		InputMode:        opts.inputMode,
		Metrics:          recorder.Report(),
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		logger.Error("write report", "error", err)
		return 1
	}
	return 0
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	opts := options{
		sqlitePath: defaultSQLitePath,
		apiURL:     "http://localhost:8080/convert",
		limit:      1000,
		seed:       42,
		batchSize:  50,
		timeout:    5 * time.Minute,
		inputMode:  inputModeAddressTown,
	}
	fs := flag.NewFlagSet("convert-eval-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.sqlitePath, "sqlite", opts.sqlitePath, "DataAddr SQLite path")
	fs.StringVar(&opts.apiURL, "api-url", opts.apiURL, "convert API URL")
	fs.StringVar(&opts.country, "country", opts.country, "optional ISO alpha-2 country filter")
	fs.IntVar(&opts.limit, "limit", opts.limit, "maximum sampled rows")
	fs.Uint64Var(&opts.seed, "seed", opts.seed, "deterministic sampling seed")
	fs.IntVar(&opts.batchSize, "batch-size", opts.batchSize, "convert API batch size")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "overall evaluation timeout")
	fs.StringVar(&opts.mismatchesPath, "mismatches", opts.mismatchesPath, "optional JSONL mismatch output path")
	fs.BoolVar(&opts.includeBlankTown, "include-blank-town", opts.includeBlankTown, "include rows with blank SQLite town")
	fs.StringVar(&opts.inputMode, "input-mode", opts.inputMode, "input text mode: address, address-town, or address-town-country")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.limit <= 0 {
		return options{}, fmt.Errorf("limit must be positive")
	}
	if opts.batchSize <= 0 {
		return options{}, fmt.Errorf("batch-size must be positive")
	}
	if opts.timeout <= 0 {
		return options{}, fmt.Errorf("timeout must be positive")
	}
	switch opts.inputMode {
	case inputModeAddress, inputModeAddressTown, inputModeAddressTownCountry:
	default:
		return options{}, fmt.Errorf("unsupported input-mode %q", opts.inputMode)
	}
	return opts, nil
}

type mismatchWriter struct {
	writer *bufio.Writer
	file   *os.File
}

func newMismatchWriter(path string) (*mismatchWriter, func() error, error) {
	if path == "" {
		writer := &mismatchWriter{}
		return writer, func() error { return nil }, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create mismatch file %q: %w", path, err)
	}
	writer := &mismatchWriter{writer: bufio.NewWriter(file), file: file}
	closeFn := func() error {
		if err := writer.writer.Flush(); err != nil {
			_ = file.Close()
			return fmt.Errorf("flush mismatch file: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close mismatch file: %w", err)
		}
		return nil
	}
	return writer, closeFn, nil
}

func (w *mismatchWriter) Write(row evaluationRow) error {
	if w == nil || w.writer == nil {
		return nil
	}
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode mismatch row: %w", err)
	}
	if _, err := w.writer.Write(data); err != nil {
		return fmt.Errorf("write mismatch row: %w", err)
	}
	if err := w.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("write mismatch newline: %w", err)
	}
	return nil
}
