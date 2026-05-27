package readers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

func TestReadTextYieldsOneSamplePerLineStrippingOnlyTrailingNewline(t *testing.T) {
	path := writeTempFile(t, "addresses.txt", "first address\r\n\r\nthird address\n")

	got, err := ReadText(path)
	if err != nil {
		t.Fatalf("ReadText() error = %v", err)
	}

	want := []core.AddressSample{
		{Text: "first address"},
		{Text: ""},
		{Text: "third address"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadText() = %#v, want %#v", got, want)
	}
}

func TestReadDelimitedReadsSuggestionColumns(t *testing.T) {
	path := writeTempFile(t, "addresses.csv", strings.Join([]string{
		"address,suggested_country,force_suggested_country",
		"10 Downing St, gb ,true",
		"1600 Pennsylvania Ave,us,0",
	}, "\n"))

	got, err := ReadDelimited(path, ',', DefaultAddressColumn)
	if err != nil {
		t.Fatalf("ReadDelimited() error = %v", err)
	}

	want := []core.AddressSample{
		{Text: "10 Downing St", SuggestedCountry: "GB", HasSuggestedCountry: true, ForceSuggestedCountry: true},
		{Text: "1600 Pennsylvania Ave", SuggestedCountry: "US", HasSuggestedCountry: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDelimited() = %#v, want %#v", got, want)
	}
}

func TestReadDelimitedNormalizesPaddedAndBOMPrefixedHeaders(t *testing.T) {
	path := writeTempFile(t, "addresses.csv", strings.Join([]string{
		"\ufeff address , suggested_country , force_suggested_country ",
		"10 Downing St, gb , y ",
	}, "\n"))

	got, err := ReadDelimited(path, ',', DefaultAddressColumn)
	if err != nil {
		t.Fatalf("ReadDelimited() error = %v", err)
	}

	want := []core.AddressSample{
		{Text: "10 Downing St", SuggestedCountry: "GB", HasSuggestedCountry: true, ForceSuggestedCountry: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDelimited() = %#v, want %#v", got, want)
	}
}

func TestReadDelimitedMissingSuggestionColumnsDefaultToNoSuggestion(t *testing.T) {
	path := writeTempFile(t, "addresses.csv", strings.Join([]string{
		"address",
		"10 Downing St",
	}, "\n"))

	got, err := ReadDelimited(path, ',', DefaultAddressColumn)
	if err != nil {
		t.Fatalf("ReadDelimited() error = %v", err)
	}

	want := []core.AddressSample{{Text: "10 Downing St"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDelimited() = %#v, want %#v", got, want)
	}
}

func TestReadDelimitedForceFlagAcceptedValues(t *testing.T) {
	path := writeTempFile(t, "addresses.csv", strings.Join([]string{
		"address,force_suggested_country",
		"a,true",
		"b,1",
		"c,YES",
		"d,Y",
		"e,no",
	}, "\n"))

	got, err := ReadDelimited(path, ',', DefaultAddressColumn)
	if err != nil {
		t.Fatalf("ReadDelimited() error = %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("ReadDelimited() returned %d samples, want 5", len(got))
	}
	for i, sample := range got[:4] {
		if !sample.ForceSuggestedCountry {
			t.Fatalf("sample %d ForceSuggestedCountry = false, want true: %#v", i, sample)
		}
	}
	if got[4].ForceSuggestedCountry {
		t.Fatalf("sample 4 ForceSuggestedCountry = true, want false: %#v", got[4])
	}
}

func TestReadDelimitedMissingAddressColumnReturnsError(t *testing.T) {
	path := writeTempFile(t, "addresses.csv", strings.Join([]string{
		"street,suggested_country",
		"10 Downing St,GB",
	}, "\n"))

	_, err := ReadDelimited(path, ',', DefaultAddressColumn)
	if err == nil {
		t.Fatal("ReadDelimited() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Column 'address' not found") {
		t.Fatalf("ReadDelimited() error = %q, want Column 'address' not found", err)
	}
}

func TestReadDelimitedSupportsTSV(t *testing.T) {
	path := writeTempFile(t, "addresses.tsv", strings.Join([]string{
		"address\tsuggested_country\tforce_suggested_country",
		"10 Downing St\tgb\ty",
	}, "\n"))

	got, err := ReadDelimited(path, '\t', DefaultAddressColumn)
	if err != nil {
		t.Fatalf("ReadDelimited() error = %v", err)
	}

	want := []core.AddressSample{
		{Text: "10 Downing St", SuggestedCountry: "GB", HasSuggestedCountry: true, ForceSuggestedCountry: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDelimited() = %#v, want %#v", got, want)
	}
}

func TestReadOpenAddressesGeoJSONBuildsFreeTextWithCountryHint(t *testing.T) {
	path := writeTempFile(t, "addresses.geojson", strings.Join([]string{
		`{"type":"Feature","properties":{"number":"101A","street":"BAYFRONT AVENUE","unit":"TEMPORARY SITE OFFICE","city":"","district":"","region":"","postcode":"018895"}}`,
		`{"type":"Feature","properties":{"number":"2","street":"PARK STREET","unit":"NIL","city":"SINGAPORE","region":"CENTRAL","postcode":"018928"}}`,
	}, "\n"))

	got, err := ReadOpenAddressesGeoJSON(path, OpenAddressesOptions{CountryCode: "sg"})
	if err != nil {
		t.Fatalf("ReadOpenAddressesGeoJSON() error = %v", err)
	}

	want := []core.AddressSample{
		{
			Text:                "101A BAYFRONT AVENUE\nTEMPORARY SITE OFFICE\n018895",
			SuggestedCountry:    "SG",
			HasSuggestedCountry: true,
		},
		{
			Text:                "2 PARK STREET\n018928 SINGAPORE\nCENTRAL",
			SuggestedCountry:    "SG",
			HasSuggestedCountry: true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadOpenAddressesGeoJSON() = %#v, want %#v", got, want)
	}
}

func TestReadOpenAddressesGeoJSONRespectsMaxRecords(t *testing.T) {
	path := writeTempFile(t, "addresses.geojson", strings.Join([]string{
		`{"type":"Feature","properties":{"number":"1","street":"A ROAD"}}`,
		`{"type":"Feature","properties":{"number":"2","street":"B ROAD"}}`,
	}, "\n"))

	got, err := ReadOpenAddressesGeoJSON(path, OpenAddressesOptions{MaxRecords: 1})
	if err != nil {
		t.Fatalf("ReadOpenAddressesGeoJSON() error = %v", err)
	}
	if len(got) != 1 || got[0].Text != "1 A ROAD" {
		t.Fatalf("ReadOpenAddressesGeoJSON() = %#v, want first record only", got)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
