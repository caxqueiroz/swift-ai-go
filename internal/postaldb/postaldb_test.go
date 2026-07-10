package postaldb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"

	_ "modernc.org/sqlite"
)

func TestValidateSeparatesCountryEvidenceFromSafeTownEvidence(t *testing.T) {
	path := createTestPostalDB(t)
	runner, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = runner.Close()
	}()

	sg, err := runner.Validate(context.Background(), "sg", "singapore", "018928")
	if err != nil {
		t.Fatalf("Validate(SG) error = %v", err)
	}
	if !sg.PostcodeExists || !sg.CountryMatches || !sg.HasCoordinates {
		t.Fatalf("SG evidence = %#v, want postcode/country/coordinates evidence", sg)
	}
	if sg.TownMatches || sg.SafeTownEvidence {
		t.Fatalf("SG evidence = %#v, postal-only row must not validate town", sg)
	}

	cl, err := runner.Validate(context.Background(), "cl", "providencia", "7500000")
	if err != nil {
		t.Fatalf("Validate(CL) error = %v", err)
	}
	if !cl.PostcodeExists || !cl.CountryMatches || !cl.TownMatches || !cl.SafeTownEvidence {
		t.Fatalf("CL evidence = %#v, want safe country and town evidence", cl)
	}
}

func TestMatchBatchReturnsCountryEvidenceButOnlySafeTownEvidence(t *testing.T) {
	path := createTestPostalDB(t)
	runner, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = runner.Close()
	}()

	got, err := runner.MatchBatch(context.Background(), []string{
		"12 PARK STREET 018928 SG",
		"AV PROVIDENCIA 7500000 CL",
	})
	if err != nil {
		t.Fatalf("MatchBatch() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("MatchBatch() returned %d rows, want 2", len(got))
	}
	if len(got[0]) != 1 {
		t.Fatalf("SG matches = %#v, want one country-only match", got[0])
	}
	if got[0][0].Matched != "018928" || got[0][0].Origin != "SG" || got[0][0].Possibility != "" {
		t.Fatalf("SG match = %#v, want country-only postcode evidence", got[0][0])
	}
	wantTown := core.PostcodeMatch{
		Matched:     "7500000",
		Possibility: "PROVIDENCIA",
		Origin:      "CL",
	}
	if len(got[1]) != 2 {
		t.Fatalf("CL matches = %#v, want country-only and safe town matches", got[1])
	}
	var foundCountry bool
	var foundTown bool
	for _, match := range got[1] {
		if match.Matched == "7500000" && match.Origin == "CL" && match.Possibility == "" {
			foundCountry = true
		}
		if match.Matched == wantTown.Matched && match.Possibility == wantTown.Possibility && match.Origin == wantTown.Origin {
			foundTown = true
		}
	}
	if !foundCountry || !foundTown {
		t.Fatalf("CL matches = %#v, want country-only and %#v", got[1], wantTown)
	}
}

func TestMatchBatchToleratesBlankAccuracyValues(t *testing.T) {
	path := createTestPostalDB(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec("UPDATE locations SET accuracy = '' WHERE country = 'CL' AND postcode = '7500000'")
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close sqlite: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("update accuracy: %v", err)
	}

	runner, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = runner.Close()
	}()

	got, err := runner.MatchBatch(context.Background(), []string{"AV PROVIDENCIA 7500000 CL"})
	if err != nil {
		t.Fatalf("MatchBatch() error = %v", err)
	}
	if len(got) != 1 || len(got[0]) == 0 {
		t.Fatalf("MatchBatch() = %#v, want postcode evidence", got)
	}
}

func createTestPostalDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "postal.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	_, err = db.Exec(`
CREATE TABLE locations (
	country TEXT NOT NULL,
	town TEXT,
	postcode TEXT,
	latitude REAL,
	longitude REAL,
	accuracy INTEGER,
	coordinates TEXT,
	source TEXT NOT NULL,
	dataaddr_rows INTEGER NOT NULL DEFAULT 0,
	postal_rows INTEGER NOT NULL DEFAULT 0,
	admin_name1 TEXT,
	admin_code1 TEXT,
	admin_name2 TEXT,
	admin_code2 TEXT,
	admin_name3 TEXT,
	admin_code3 TEXT
);
CREATE INDEX idx_locations_country_postcode ON locations(country, postcode);
CREATE INDEX idx_locations_postcode ON locations(postcode);
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	insertLocation(t, db, "SG", "PARK STREET", "018928", "postal_codes", 0, 1, 1.2777, 103.8503)
	insertLocation(t, db, "CL", "PROVIDENCIA", "7500000", "both_town", 17652, 1, -33.4362, -70.609)
	return path
}

func insertLocation(t *testing.T, db *sql.DB, country, town, postcode, source string, dataaddrRows, postalRows int, lat, lon float64) {
	t.Helper()
	_, err := db.Exec(`
INSERT INTO locations (
	country, town, postcode, latitude, longitude, source, dataaddr_rows, postal_rows
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		country, town, postcode, lat, lon, source, dataaddrRows, postalRows)
	if err != nil {
		t.Fatalf("insert location: %v", err)
	}
}
