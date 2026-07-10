package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	inventoryStatusReady        = "ready"
	inventoryStatusMissingShard = "missing_shard"
	inventoryStatusInvalidShard = "invalid_shard"
	inventoryStatusUnavailable  = "unavailable"
)

type inventoryReport struct {
	Root      string             `json:"root"`
	ShardsDir string             `json:"shards_dir"`
	Summary   inventorySummary   `json:"summary"`
	Countries []countryInventory `json:"countries"`
}

type inventorySummary struct {
	Total        int `json:"total"`
	Ready        int `json:"ready"`
	MissingShard int `json:"missing_shard"`
	InvalidShard int `json:"invalid_shard"`
	Unavailable  int `json:"unavailable"`
}

type countryInventory struct {
	Country      string `json:"country"`
	Status       string `json:"status"`
	SourceExists bool   `json:"source_exists"`
	ShardExists  bool   `json:"shard_exists"`
	ShardPath    string `json:"shard_path,omitempty"`
	RowCount     int64  `json:"row_count,omitempty"`
	TownCount    int64  `json:"town_count,omitempty"`
	Error        string `json:"error,omitempty"`
}

func buildInventoryReport(ctx context.Context, sqlitePath string, countries []string) (inventoryReport, error) {
	root := dataAddrRootForSQLite(sqlitePath)
	shardsDir := filepath.Join(root, "address_shards")
	filter := countryFilterSet(countries)

	sourceCountries, err := sourceCountryDirs(root)
	if err != nil {
		return inventoryReport{}, err
	}
	shardCountries, err := shardCountryFiles(shardsDir)
	if err != nil {
		return inventoryReport{}, err
	}

	countrySet := make(map[string]struct{}, len(sourceCountries)+len(shardCountries))
	for country := range sourceCountries {
		countrySet[country] = struct{}{}
	}
	for country := range shardCountries {
		countrySet[country] = struct{}{}
	}

	countryList := make([]string, 0, len(countrySet))
	for country := range countrySet {
		if len(filter) > 0 {
			if _, ok := filter[country]; !ok {
				continue
			}
		}
		countryList = append(countryList, country)
	}
	sort.Strings(countryList)

	report := inventoryReport{
		Root:      root,
		ShardsDir: shardsDir,
		Countries: make([]countryInventory, 0, len(countryList)),
	}
	for _, country := range countryList {
		row := countryInventory{
			Country:      strings.ToUpper(country),
			SourceExists: sourceCountries[country],
			ShardPath:    filepath.Join(shardsDir, country+".sqlite"),
		}
		if _, ok := shardCountries[country]; !ok {
			row.Status = inventoryStatusMissingShard
			report.Countries = append(report.Countries, row)
			report.Summary.MissingShard++
			continue
		}

		row.ShardExists = true
		rowCount, townCount, err := shardCounts(ctx, row.ShardPath)
		row.RowCount = rowCount
		row.TownCount = townCount
		if err != nil {
			row.Status = inventoryStatusUnavailable
			row.Error = err.Error()
			report.Summary.Unavailable++
		} else if rowCount <= 0 || townCount <= 0 {
			row.Status = inventoryStatusInvalidShard
			row.Error = "shard has no rows or no non-empty towns"
			report.Summary.InvalidShard++
		} else {
			row.Status = inventoryStatusReady
			report.Summary.Ready++
		}
		report.Countries = append(report.Countries, row)
	}
	report.Summary.Total = len(report.Countries)
	return report, nil
}

func dataAddrRootForSQLite(sqlitePath string) string {
	dir := filepath.Dir(sqlitePath)
	if filepath.Base(dir) == "address_shards" {
		return filepath.Dir(dir)
	}
	return dir
}

func sourceCountryDirs(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read DataAddr root %q: %w", root, err)
	}
	countries := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		country := strings.ToLower(entry.Name())
		if isCountryCode(country) {
			countries[country] = true
		}
	}
	return countries, nil
}

func shardCountryFiles(shardsDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(shardsDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read shard dir %q: %w", shardsDir, err)
	}
	countries := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sqlite" {
			continue
		}
		country := strings.ToLower(strings.TrimSuffix(entry.Name(), ".sqlite"))
		if isCountryCode(country) {
			countries[country] = true
		}
	}
	return countries, nil
}

func shardCounts(ctx context.Context, path string) (int64, int64, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, 0, fmt.Errorf("open shard: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()
	row := db.QueryRowContext(ctx, `
SELECT
	count(*) AS row_count,
	count(DISTINCT CASE WHEN town IS NOT NULL AND trim(town) <> '' THEN town END) AS town_count
FROM addresses`)
	var rowCount int64
	var townCount int64
	if err := row.Scan(&rowCount, &townCount); err != nil {
		return 0, 0, fmt.Errorf("count shard: %w", err)
	}
	return rowCount, townCount, nil
}

func countryFilterSet(countries []string) map[string]struct{} {
	filter := make(map[string]struct{})
	for _, country := range countries {
		country = strings.ToLower(canonicalCountry(country))
		if country != "" {
			filter[country] = struct{}{}
		}
	}
	return filter
}

func splitCountries(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	countries := make([]string, 0, len(fields))
	for _, field := range fields {
		country := canonicalCountry(field)
		if country != "" {
			countries = append(countries, country)
		}
	}
	return countries
}

func isCountryCode(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
