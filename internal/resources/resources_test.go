package resources

import (
	"compress/zlib"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
)

type townFixtureRow struct {
	Name        string `parquet:"name"`
	CountryCode string `parquet:"country code"`
	Population  int    `parquet:"population"`
}

func TestLoadCompressedJSONDecodesZlibJSON(t *testing.T) {
	path := writeCompressedJSON(t, "country-aliases.json.zlib", map[string][]string{
		"FR": {"FRANCE"},
	})

	got, err := LoadCompressedJSON[map[string][]string](path)
	if err != nil {
		t.Fatalf("LoadCompressedJSON() error = %v", err)
	}

	want := map[string][]string{"FR": {"FRANCE"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadCompressedJSON() = %#v, want %#v", got, want)
	}
}

func TestLoadJSONDecodesPlainJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country-groupings.json")
	if err := os.WriteFile(path, []byte(`{"EU":["FR","DE"],"NA":["US"]}`), 0o600); err != nil {
		t.Fatalf("write JSON fixture: %v", err)
	}

	got, err := LoadJSON[map[string][]string](path)
	if err != nil {
		t.Fatalf("LoadJSON() error = %v", err)
	}

	want := map[string][]string{
		"EU": {"FR", "DE"},
		"NA": {"US"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadJSON() = %#v, want %#v", got, want)
	}
}

func TestLoadCompressedJSONReturnsContextForDecodeErrors(t *testing.T) {
	t.Run("invalid zlib", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid-zlib.json.zlib")
		if err := os.WriteFile(path, []byte("not a zlib stream"), 0o600); err != nil {
			t.Fatalf("write invalid zlib fixture: %v", err)
		}

		_, err := LoadCompressedJSON[map[string][]string](path)
		if err == nil {
			t.Fatal("LoadCompressedJSON() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "open zlib stream") {
			t.Fatalf("error = %q, want context containing %q", err.Error(), "open zlib stream")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		path := writeCompressedBytes(t, "invalid-json.json.zlib", []byte(`{"FR":`))

		_, err := LoadCompressedJSON[map[string][]string](path)
		if err == nil {
			t.Fatal("LoadCompressedJSON() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "decode compressed JSON") {
			t.Fatalf("error = %q, want context containing %q", err.Error(), "decode compressed JSON")
		}
	})
}

func TestLoadCountryAliasesFlattensNormalizedPossibilities(t *testing.T) {
	countryPath := writeCompressedJSON(t, "country-aliases.json.zlib", map[string][]string{
		"FR": {"France", "Republique-Francaise"},
		"US": {"United States"},
	})
	provincePath := writeCompressedJSON(t, "province-aliases.json.zlib", map[string][]string{
		"FR": {"Saint-Pierre"},
		"US": {"New-York"},
	})

	alpha2, possibilities, err := LoadCountryAliases(countryPath, provincePath)
	if err != nil {
		t.Fatalf("LoadCountryAliases() error = %v", err)
	}

	wantAlpha2 := map[string]string{
		"FR": "FRANCE",
		"US": "UNITED STATES",
	}
	if !reflect.DeepEqual(alpha2, wantAlpha2) {
		t.Fatalf("alpha2 = %#v, want %#v", alpha2, wantAlpha2)
	}

	assertStringSlice(t, possibilities["FRANCE"], []string{"FR"})
	assertStringSlice(t, possibilities["REPUBLIQUE-FRANCAISE"], []string{"FR"})
	assertStringSlice(t, possibilities["REPUBLIQUE FRANCAISE"], []string{"FR"})
	assertStringSlice(t, possibilities["SAINT-PIERRE"], []string{"FR"})
	assertStringSlice(t, possibilities["SAINT PIERRE"], []string{"FR"})
	assertStringSlice(t, possibilities["ST-PIERRE"], []string{"FR"})
	assertStringSlice(t, possibilities["NEW YORK"], []string{"US"})
	assertStringSlice(t, possibilities["NO COUNTRY"], []string{""})
}

func TestLoadCountryAliasesSortsAndDeduplicatesMultipleOrigins(t *testing.T) {
	countryPath := writeCompressedJSON(t, "country-aliases.json.zlib", map[string][]string{
		"ZZ": {"Shared"},
		"AA": {"SHARED", "Shared"},
	})
	provincePath := writeCompressedJSON(t, "province-aliases.json.zlib", map[string][]string{
		"ZZ": {"Shared"},
	})

	_, possibilities, err := LoadCountryAliases(countryPath, provincePath)
	if err != nil {
		t.Fatalf("LoadCountryAliases() error = %v", err)
	}

	assertStringSlice(t, possibilities["SHARED"], []string{"AA", "ZZ"})
}

func TestLoadTownsFromParquetBuildsTownIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "towns.parquet")
	rows := []townFixtureRow{
		{Name: "Saint-Pierre", CountryCode: "FR", Population: 5_000},
		{Name: "Saint Pierre", CountryCode: "CA", Population: 7_000},
		{Name: "Quebec", CountryCode: "CA", Population: 8_000},
		{Name: "Quebec", CountryCode: "FR", Population: 1_000},
	}
	if err := parquet.WriteFile(path, rows); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}

	aliases := map[string][]string{
		"SAINT-PIERRE": {"ST PIERRE"},
		"QUEBEC":       {"Québec City"},
	}

	possibilities, populations, largest, err := LoadTownsFromParquet(path, aliases)
	if err != nil {
		t.Fatalf("LoadTownsFromParquet() error = %v", err)
	}

	assertStringSetContains(t, possibilities["FR"], "SAINT-PIERRE", "SAINT PIERRE", "ST-PIERRE", "ST PIERRE")
	assertStringSetContains(t, possibilities["CA"], "SAINT-PIERRE", "SAINT PIERRE", "ST. PIERRE", "QUEBEC", "QUEBEC CITY")

	if got, want := populations["SAINT-PIERRE"], 7_000; got != want {
		t.Fatalf("population SAINT-PIERRE = %d, want %d", got, want)
	}
	if got, want := populations["QUEBEC"], 8_000; got != want {
		t.Fatalf("population QUEBEC = %d, want %d", got, want)
	}
	if got, want := populations["ST PIERRE"], 7_000; got != want {
		t.Fatalf("population ST PIERRE = %d, want %d", got, want)
	}
	if got, want := populations["QUEBEC CITY"], 8_000; got != want {
		t.Fatalf("population QUEBEC CITY = %d, want %d", got, want)
	}
	if got, want := largest["SAINT-PIERRE"], "CA"; got != want {
		t.Fatalf("largest SAINT-PIERRE = %q, want %q", got, want)
	}
	if got, want := largest["QUEBEC"], "CA"; got != want {
		t.Fatalf("largest QUEBEC = %q, want %q", got, want)
	}
	if got, want := largest["ST PIERRE"], "CA"; got != want {
		t.Fatalf("largest ST PIERRE = %q, want %q", got, want)
	}
	if got, want := largest["QUEBEC CITY"], "CA"; got != want {
		t.Fatalf("largest QUEBEC CITY = %q, want %q", got, want)
	}
}

func writeCompressedJSON(t *testing.T, name string, value map[string][]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create compressed fixture: %v", err)
	}
	defer file.Close()

	zw := zlib.NewWriter(file)
	if err := json.NewEncoder(zw).Encode(value); err != nil {
		t.Fatalf("encode fixture JSON: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}

	return path
}

func writeCompressedBytes(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create compressed fixture: %v", err)
	}
	defer file.Close()

	zw := zlib.NewWriter(file)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("write compressed fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}

	return path
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
}

func assertStringSetContains(t *testing.T, got map[string]struct{}, values ...string) {
	t.Helper()

	for _, value := range values {
		if _, ok := got[value]; !ok {
			t.Fatalf("set missing %q: %#v", value, got)
		}
	}
}
