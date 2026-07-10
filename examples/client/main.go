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

	"github.com/tipmarket/swift-ai/internal/holdout"
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
	requestTimeout   time.Duration
	mismatchesPath   string
	logAll           bool
	includeBlankTown bool
	inputMode        string
	holdout          holdout.Filter
	inventory        bool
	sweep            bool
	countries        string
	maxCountries     int
	outputDir        string
}

type commandReport struct {
	SQLite           string       `json:"sqlite"`
	APIURL           string       `json:"api_url"`
	Country          string       `json:"country,omitempty"`
	RequestedLimit   int          `json:"requested_limit"`
	Sampled          int          `json:"sampled"`
	Seed             uint64       `json:"seed"`
	BatchSize        int          `json:"batch_size"`
	Timeout          string       `json:"timeout"`
	RequestTimeout   string       `json:"request_timeout"`
	Mismatches       int          `json:"mismatches"`
	MismatchesPath   string       `json:"mismatches_path,omitempty"`
	LogAll           bool         `json:"log_all"`
	LoggedRows       int          `json:"logged_rows"`
	IncludeBlankTown bool         `json:"include_blank_town"`
	InputMode        string       `json:"input_mode"`
	HoldoutMod       int          `json:"holdout_mod,omitempty"`
	HoldoutMinBucket int          `json:"holdout_min_bucket,omitempty"`
	HoldoutMaxBucket int          `json:"holdout_max_bucket,omitempty"`
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
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}

	switch {
	case opts.inventory:
		report, err := buildInventoryReport(ctx, opts.sqlitePath, splitCountries(opts.countries))
		if err != nil {
			logger.Error("build inventory", "error", err)
			return 1
		}
		return writeJSONReport(stdout, report, logger)
	case opts.sweep:
		report, err := runSweep(ctx, opts, logger)
		if err != nil {
			logger.Error("run sweep", "error", err)
			return 1
		}
		return writeJSONReport(stdout, report, logger)
	}

	report, err := runEvaluation(ctx, opts, logger)
	if err != nil {
		logger.Error("run evaluation", "error", err)
		return 1
	}
	return writeJSONReport(stdout, report, logger)
}

func runEvaluation(ctx context.Context, opts options, logger *slog.Logger) (commandReport, error) {
	sqlitePath, err := resolveCountrySQLitePath(opts.sqlitePath, opts.country)
	if err != nil {
		return commandReport{}, fmt.Errorf("resolve sqlite: %w", err)
	}
	opts.sqlitePath = sqlitePath

	db, err := openSQLite(opts.sqlitePath)
	if err != nil {
		return commandReport{}, fmt.Errorf("open sqlite: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	groups, err := loadTownGroups(ctx, db, opts.country, opts.includeBlankTown)
	if err != nil {
		return commandReport{}, fmt.Errorf("load town groups: %w", err)
	}
	if len(groups) == 0 {
		return commandReport{}, fmt.Errorf("no town groups found for country %s", opts.country)
	}

	rng := rand.New(rand.NewPCG(opts.seed, opts.seed^0x9e3779b97f4a7c15))
	sampleLimit := opts.limit
	if opts.holdout.Enabled() {
		sampleLimit = opts.holdout.SampleLimit(opts.limit)
	}
	plans := planSamples(groups, sampleLimit, rng)
	records, err := loadSampledAddresses(ctx, db, plans)
	if err != nil {
		return commandReport{}, fmt.Errorf("load sampled addresses: %w", err)
	}
	records = applyHoldoutFilter(records, opts.holdout, opts.limit)

	mismatches, closeMismatches, err := newMismatchWriter(opts.mismatchesPath)
	if err != nil {
		return commandReport{}, fmt.Errorf("open mismatches: %w", err)
	}
	defer func() {
		if err := closeMismatches(); err != nil {
			logger.Error("close mismatches", "error", err)
		}
	}()

	client := &http.Client{Timeout: opts.requestTimeout}
	recorder := newMetricRecorder()
	mismatchCount := 0
	loggedRows := 0
	for start := 0; start < len(records); start += opts.batchSize {
		end := min(start+opts.batchSize, len(records))
		rows, err := convertBatch(ctx, client, opts.apiURL, records[start:end], opts.inputMode)
		if err != nil {
			return commandReport{}, fmt.Errorf("convert batch start=%d end=%d: %w", start, end, err)
		}
		for _, row := range rows {
			recorder.Add(row)
			if isMismatch(row) {
				mismatchCount++
			}
			if opts.mismatchesPath != "" && shouldLogRow(row, opts.logAll) {
				if err := mismatches.Write(row); err != nil {
					return commandReport{}, fmt.Errorf("write evaluation row: %w", err)
				}
				loggedRows++
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
		Timeout:          opts.timeout.String(),
		RequestTimeout:   opts.requestTimeout.String(),
		Mismatches:       mismatchCount,
		MismatchesPath:   opts.mismatchesPath,
		LogAll:           opts.logAll,
		LoggedRows:       loggedRows,
		IncludeBlankTown: opts.includeBlankTown,
		InputMode:        opts.inputMode,
		HoldoutMod:       opts.holdout.Mod,
		HoldoutMinBucket: opts.holdout.MinBucket,
		HoldoutMaxBucket: opts.holdout.MaxBucket,
		Metrics:          recorder.Report(),
	}
	return report, nil
}

func writeJSONReport(stdout io.Writer, report any, logger *slog.Logger) int {
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
		sqlitePath:     defaultSQLitePath,
		apiURL:         "http://127.0.0.1:8080/convert",
		limit:          1000,
		seed:           42,
		batchSize:      50,
		timeout:        5 * time.Minute,
		requestTimeout: 5 * time.Minute,
		inputMode:      inputModeAddressTown,
	}
	fs := flag.NewFlagSet("convert-eval-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.sqlitePath, "sqlite", opts.sqlitePath, "DataAddr SQLite path")
	fs.StringVar(&opts.apiURL, "api-url", opts.apiURL, "convert API URL")
	fs.StringVar(&opts.country, "country", opts.country, "optional ISO alpha-2 country filter")
	fs.IntVar(&opts.limit, "limit", opts.limit, "maximum sampled rows")
	fs.Uint64Var(&opts.seed, "seed", opts.seed, "deterministic sampling seed")
	fs.IntVar(&opts.batchSize, "batch-size", opts.batchSize, "convert API batch size")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "overall evaluation timeout; 0 disables the run-wide deadline")
	fs.DurationVar(&opts.requestTimeout, "request-timeout", opts.requestTimeout, "per-batch HTTP request timeout")
	fs.StringVar(&opts.mismatchesPath, "mismatches", opts.mismatchesPath, "optional JSONL mismatch output path")
	fs.BoolVar(&opts.logAll, "log-all", opts.logAll, "write all evaluated rows to --mismatches instead of only mismatches")
	fs.BoolVar(&opts.includeBlankTown, "include-blank-town", opts.includeBlankTown, "include rows with blank SQLite town")
	fs.StringVar(&opts.inputMode, "input-mode", opts.inputMode, "input text mode: address, address-town, or address-town-country")
	fs.IntVar(&opts.holdout.Mod, "holdout-mod", opts.holdout.Mod, "optional deterministic ID holdout modulus; 0 disables holdout filtering")
	fs.IntVar(&opts.holdout.MinBucket, "holdout-min-bucket", opts.holdout.MinBucket, "minimum stable ID bucket to include when --holdout-mod is set")
	fs.IntVar(&opts.holdout.MaxBucket, "holdout-max-bucket", opts.holdout.MaxBucket, "exclusive maximum stable ID bucket to include when --holdout-mod is set; defaults to holdout-mod")
	fs.BoolVar(&opts.inventory, "inventory", opts.inventory, "inventory DataAddr country shards and exit")
	fs.BoolVar(&opts.sweep, "sweep", opts.sweep, "run evaluation across ready country shards")
	fs.StringVar(&opts.countries, "countries", opts.countries, "comma or space separated country list for inventory/sweep")
	fs.IntVar(&opts.maxCountries, "max-countries", opts.maxCountries, "maximum ready countries to evaluate during sweep; 0 means all")
	fs.StringVar(&opts.outputDir, "output-dir", opts.outputDir, "optional sweep output directory for per-country JSONL rows")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.limit <= 0 {
		return options{}, fmt.Errorf("limit must be positive")
	}
	if opts.batchSize <= 0 {
		return options{}, fmt.Errorf("batch-size must be positive")
	}
	if opts.timeout < 0 {
		return options{}, fmt.Errorf("timeout must be non-negative")
	}
	if opts.requestTimeout <= 0 {
		return options{}, fmt.Errorf("request-timeout must be positive")
	}
	switch opts.inputMode {
	case inputModeAddress, inputModeAddressTown, inputModeAddressTownCountry:
	default:
		return options{}, fmt.Errorf("unsupported input-mode %q", opts.inputMode)
	}
	if opts.holdout.Mod < 0 {
		return options{}, fmt.Errorf("holdout-mod must be non-negative")
	}
	if opts.maxCountries < 0 {
		return options{}, fmt.Errorf("max-countries must be non-negative")
	}
	if opts.inventory && opts.sweep {
		return options{}, fmt.Errorf("inventory and sweep are mutually exclusive")
	}
	if opts.holdout.Enabled() {
		maxBucket := opts.holdout.EffectiveMaxBucket()
		if opts.holdout.MaxBucket < 0 || maxBucket > opts.holdout.Mod {
			return options{}, fmt.Errorf("holdout-max-bucket must be between 1 and holdout-mod")
		}
		if opts.holdout.MinBucket < 0 || opts.holdout.MinBucket >= maxBucket {
			return options{}, fmt.Errorf("holdout-min-bucket must be between 0 and holdout-max-bucket - 1")
		}
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
