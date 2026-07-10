package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLoadTownGroupsFiltersCountryAndBlankTowns(t *testing.T) {
	db := openTestSQLite(t)
	insertAddress(t, db, "1", "1 PARK STREET", "Singapore", "sg")
	insertAddress(t, db, "2", "2 PARK STREET", "Singapore", "sg")
	insertAddress(t, db, "3", "3 QUEENSWAY", "Queenstown", "sg")
	insertAddress(t, db, "4", "4 BLANK", "", "sg")
	insertAddress(t, db, "5", "5 RIVOLI", "Paris", "fr")

	groups, err := loadTownGroups(context.Background(), db, "sg", false)
	if err != nil {
		t.Fatalf("loadTownGroups() error = %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %#v, want two SG non-blank towns", groups)
	}
	want := map[string]int{"Singapore": 2, "Queenstown": 1}
	for _, group := range groups {
		if group.Country != "SG" {
			t.Fatalf("country = %q, want SG", group.Country)
		}
		if want[group.Town] != group.Count {
			t.Fatalf("group %#v not in expected counts %#v", group, want)
		}
	}
}

func TestFetchAddressAtPlanOffset(t *testing.T) {
	db := openTestSQLite(t)
	insertAddress(t, db, "1", "1 PARK STREET", "Singapore", "sg")
	insertAddress(t, db, "2", "2 PARK STREET", "Singapore", "sg")

	record, err := fetchAddressAtPlanOffset(context.Background(), db, samplePlan{
		Country: "SG",
		Town:    "Singapore",
		Stratum: "SG:common",
		Offset:  1,
	})
	if err != nil {
		t.Fatalf("fetchAddressAtPlanOffset() error = %v", err)
	}
	if record.ID != "2" || record.Address != "2 PARK STREET" || record.Town != "Singapore" || record.Country != "SG" {
		t.Fatalf("record = %#v, want second Singapore row", record)
	}
}

func TestResolveCountrySQLitePathUsesSiblingCountryShard(t *testing.T) {
	root := t.TempDir()
	aggregate := filepath.Join(root, "addresses.sqlite")
	shardDir := filepath.Join(root, "address_shards")
	shard := filepath.Join(shardDir, "cl.sqlite")
	if err := os.WriteFile(aggregate, []byte("aggregate"), 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		t.Fatalf("create shard dir: %v", err)
	}
	if err := os.WriteFile(shard, []byte("shard"), 0o644); err != nil {
		t.Fatalf("write shard: %v", err)
	}

	got, err := resolveCountrySQLitePath(aggregate, "CL")
	if err != nil {
		t.Fatalf("resolveCountrySQLitePath() error = %v", err)
	}
	if got != shard {
		t.Fatalf("resolved path = %q, want %q", got, shard)
	}
}

func TestResolveCountrySQLitePathKeepsOriginalWhenShardMissing(t *testing.T) {
	root := t.TempDir()
	aggregate := filepath.Join(root, "addresses.sqlite")
	if err := os.WriteFile(aggregate, []byte("aggregate"), 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}

	got, err := resolveCountrySQLitePath(aggregate, "CL")
	if err != nil {
		t.Fatalf("resolveCountrySQLitePath() error = %v", err)
	}
	if got != aggregate {
		t.Fatalf("resolved path = %q, want original %q", got, aggregate)
	}
}

func openTestSQLite(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
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
	return db
}

func insertAddress(t *testing.T, db *sql.DB, id string, address string, town string, country string) {
	t.Helper()

	_, err := db.Exec(`INSERT INTO addresses (id, address, town, country) VALUES (?, ?, ?, ?)`, id, address, town, country)
	if err != nil {
		t.Fatalf("insert address: %v", err)
	}
}
