package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type sweepReport struct {
	SQLite         string               `json:"sqlite"`
	APIURL         string               `json:"api_url"`
	RequestedLimit int                  `json:"requested_limit"`
	Seed           uint64               `json:"seed"`
	BatchSize      int                  `json:"batch_size"`
	InputMode      string               `json:"input_mode"`
	Inventory      inventorySummary     `json:"inventory"`
	Summary        sweepSummary         `json:"summary"`
	Countries      []sweepCountryReport `json:"countries"`
}

type sweepSummary struct {
	Total      int `json:"total"`
	Evaluated  int `json:"evaluated"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
	Mismatches int `json:"mismatches"`
	Sampled    int `json:"sampled"`
	Committed  int `json:"committed"`
}

type sweepCountryReport struct {
	Country    string           `json:"country"`
	Status     string           `json:"status"`
	Error      string           `json:"error,omitempty"`
	Inventory  countryInventory `json:"inventory"`
	Evaluation *commandReport   `json:"evaluation,omitempty"`
}

func runSweep(ctx context.Context, opts options, logger *slog.Logger) (sweepReport, error) {
	inventory, err := buildInventoryReport(ctx, opts.sqlitePath, splitCountries(opts.countries))
	if err != nil {
		return sweepReport{}, err
	}
	if opts.outputDir != "" {
		if err := os.MkdirAll(opts.outputDir, 0o755); err != nil {
			return sweepReport{}, fmt.Errorf("create output dir %q: %w", opts.outputDir, err)
		}
	}

	ready := make([]countryInventory, 0, len(inventory.Countries))
	for _, country := range inventory.Countries {
		if country.Status == inventoryStatusReady {
			ready = append(ready, country)
		}
	}
	if opts.maxCountries > 0 && len(ready) > opts.maxCountries {
		ready = ready[:opts.maxCountries]
	}

	report := sweepReport{
		SQLite:         opts.sqlitePath,
		APIURL:         opts.apiURL,
		RequestedLimit: opts.limit,
		Seed:           opts.seed,
		BatchSize:      opts.batchSize,
		InputMode:      opts.inputMode,
		Inventory:      inventory.Summary,
		Countries:      make([]sweepCountryReport, 0, len(inventory.Countries)),
	}

	readyByCountry := make(map[string]countryInventory, len(ready))
	for _, country := range ready {
		readyByCountry[country.Country] = country
	}

	for _, country := range inventory.Countries {
		if _, ok := readyByCountry[country.Country]; ok {
			continue
		}
		report.Countries = append(report.Countries, sweepCountryReport{
			Country:   country.Country,
			Status:    "skipped",
			Inventory: country,
		})
		report.Summary.Skipped++
	}

	for _, country := range ready {
		evalOpts := opts
		evalOpts.country = country.Country
		evalOpts.inventory = false
		evalOpts.sweep = false
		if opts.outputDir != "" {
			evalOpts.mismatchesPath = filepath.Join(opts.outputDir, strings.ToLower(country.Country)+".jsonl")
		}

		evaluation, err := runEvaluation(ctx, evalOpts, logger)
		row := sweepCountryReport{
			Country:   country.Country,
			Inventory: country,
		}
		if err != nil {
			row.Status = "failed"
			row.Error = err.Error()
			report.Summary.Failed++
			report.Countries = append(report.Countries, row)
			continue
		}
		row.Status = "evaluated"
		row.Evaluation = &evaluation
		report.Summary.Evaluated++
		report.Summary.Sampled += evaluation.Sampled
		report.Summary.Mismatches += evaluation.Mismatches
		report.Summary.Committed += evaluation.Metrics.Committed
		report.Countries = append(report.Countries, row)
	}

	report.Summary.Total = len(report.Countries)
	if report.Summary.Evaluated == 0 && len(ready) == 0 {
		return report, fmt.Errorf("no ready country shards found")
	}
	return report, nil
}
