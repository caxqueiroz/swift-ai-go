package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

func TestDecodeBIOTagsAcceptsStructuredConfig(t *testing.T) {
	raw := json.RawMessage(`[
		{"tag":"OTHER","bio":"OTHER"},
		{"tag":"COUNTRY","bio":"B-"},
		{"tag":"COUNTRY","bio":"I-"}
	]`)

	got, err := DecodeBIOTags(raw)
	if err != nil {
		t.Fatalf("DecodeBIOTags() error = %v", err)
	}

	want := []core.BIOTag{
		{Tag: core.TagOther, BIO: core.BioOther},
		{Tag: core.TagCountry, BIO: core.BioBefore},
		{Tag: core.TagCountry, BIO: core.BioInside},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeBIOTags() = %#v, want %#v", got, want)
	}
}

func TestDecodeBIOTagsAcceptsStringConfig(t *testing.T) {
	raw := json.RawMessage(`["OTHER","B-COUNTRY","I-TOWN"]`)

	got, err := DecodeBIOTags(raw)
	if err != nil {
		t.Fatalf("DecodeBIOTags() error = %v", err)
	}

	want := []core.BIOTag{
		{Tag: core.TagOther, BIO: core.BioOther},
		{Tag: core.TagCountry, BIO: core.BioBefore},
		{Tag: core.TagTown, BIO: core.BioInside},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeBIOTags() = %#v, want %#v", got, want)
	}
}

func TestLoadCRFConfigAcceptsLegacyTransitionNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "address_crf.json")
	data := []byte(`{
		"start_transitions": [0.1, 0.2],
		"end_transitions": [0.3, 0.4],
		"transitions": [[0.1, 0.2], [0.3, 0.4]],
		"transitions_order_2": [[0.5, 0.6], [0.7, 0.8]]
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write CRF fixture: %v", err)
	}

	got, err := LoadCRFConfig(path)
	if err != nil {
		t.Fatalf("LoadCRFConfig() error = %v", err)
	}

	if !reflect.DeepEqual(got.Start, []float64{0.1, 0.2}) {
		t.Fatalf("Start = %#v, want legacy start transitions", got.Start)
	}
	if !reflect.DeepEqual(got.End, []float64{0.3, 0.4}) {
		t.Fatalf("End = %#v, want legacy end transitions", got.End)
	}
}
