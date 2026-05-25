package core

import "testing"

func TestTagStringValuesMatchPython(t *testing.T) {
	if TagCountry.String() != "COUNTRY" {
		t.Fatalf("country tag = %q", TagCountry.String())
	}
	if TagTown.String() != "TOWN" {
		t.Fatalf("town tag = %q", TagTown.String())
	}
	if BioBefore.String() != "B-" {
		t.Fatalf("before bio = %q", BioBefore.String())
	}
}

func TestAddressSampleCarriesSuggestionMetadata(t *testing.T) {
	sample := AddressSample{Text: "PARIS FR", SuggestedCountry: "FR", HasSuggestedCountry: true, ForceSuggestedCountry: true}
	if sample.Text != "PARIS FR" || sample.SuggestedCountry != "FR" || !sample.HasSuggestedCountry || !sample.ForceSuggestedCountry {
		t.Fatalf("unexpected sample: %#v", sample)
	}
}
