package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBuildInventoryReportClassifiesReadyMissingAndInvalidShards(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "cl"))
	mustMkdir(t, filepath.Join(root, "fr"))
	mustMkdir(t, filepath.Join(root, "sg"))
	shardsDir := filepath.Join(root, "address_shards")
	mustMkdir(t, shardsDir)
	createShardSQLite(t, filepath.Join(shardsDir, "cl.sqlite"), []addressRecord{
		{ID: "1", Address: "1 MAIN", Town: "SANTIAGO", Country: "cl"},
		{ID: "2", Address: "2 MAIN", Town: "PROVIDENCIA", Country: "cl"},
	})
	createShardSQLite(t, filepath.Join(shardsDir, "sg.sqlite"), []addressRecord{
		{ID: "3", Address: "3 MAIN", Town: "", Country: "sg"},
	})

	report, err := buildInventoryReport(context.Background(), filepath.Join(root, "addresses.sqlite"), nil)
	if err != nil {
		t.Fatalf("buildInventoryReport() error = %v", err)
	}

	byCountry := inventoryByCountry(report.Countries)
	if byCountry["CL"].Status != inventoryStatusReady || byCountry["CL"].RowCount != 2 || byCountry["CL"].TownCount != 2 {
		t.Fatalf("CL inventory = %#v, want ready with counts", byCountry["CL"])
	}
	if byCountry["FR"].Status != inventoryStatusMissingShard {
		t.Fatalf("FR status = %q, want missing_shard", byCountry["FR"].Status)
	}
	if byCountry["SG"].Status != inventoryStatusInvalidShard {
		t.Fatalf("SG status = %q, want invalid_shard", byCountry["SG"].Status)
	}
	if report.Summary.Ready != 1 || report.Summary.MissingShard != 1 || report.Summary.InvalidShard != 1 {
		t.Fatalf("summary = %#v, want one ready/missing/invalid", report.Summary)
	}
}

func TestBuildInventoryReportFiltersCountries(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "cl"))
	mustMkdir(t, filepath.Join(root, "fr"))
	shardsDir := filepath.Join(root, "address_shards")
	mustMkdir(t, shardsDir)
	createShardSQLite(t, filepath.Join(shardsDir, "cl.sqlite"), []addressRecord{
		{ID: "1", Address: "1 MAIN", Town: "SANTIAGO", Country: "cl"},
	})

	report, err := buildInventoryReport(context.Background(), filepath.Join(root, "addresses.sqlite"), []string{"CL"})
	if err != nil {
		t.Fatalf("buildInventoryReport() error = %v", err)
	}
	if len(report.Countries) != 1 || report.Countries[0].Country != "CL" {
		t.Fatalf("countries = %#v, want only CL", report.Countries)
	}
}

func inventoryByCountry(rows []countryInventory) map[string]countryInventory {
	byCountry := make(map[string]countryInventory, len(rows))
	for _, row := range rows {
		byCountry[row.Country] = row
	}
	return byCountry
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func createShardSQLite(t *testing.T, path string, records []addressRecord) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open shard sqlite: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	_, err = db.Exec(`
CREATE TABLE addresses (
	id TEXT PRIMARY KEY,
	address TEXT NOT NULL,
	town TEXT,
	country TEXT NOT NULL,
	postcode TEXT
)`)
	if err != nil {
		t.Fatalf("create addresses table: %v", err)
	}
	for _, record := range records {
		_, err := db.Exec(`INSERT INTO addresses (id, address, town, country) VALUES (?, ?, ?, ?)`,
			record.ID, record.Address, record.Town, record.Country)
		if err != nil {
			t.Fatalf("insert address: %v", err)
		}
	}
}
