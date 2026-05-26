package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/readers"
	isoruntime "github.com/tipmarket/swift-ai/internal/runtime"
)

type parityExpected struct {
	Address string `json:"address"`
	Country string `json:"country"`
	Town    string `json:"town"`
}

func TestParityAgainstPythonFixtures(t *testing.T) {
	resourcesDir := os.Getenv("ISO20022_RESOURCES_DIR")
	modelDir := os.Getenv("ISO20022_MODEL_DIR")
	expectedPath := os.Getenv("ISO20022_EXPECTED_PARITY_JSON")
	if resourcesDir == "" || modelDir == "" || expectedPath == "" {
		t.Skip("set ISO20022_RESOURCES_DIR, ISO20022_MODEL_DIR, and ISO20022_EXPECTED_PARITY_JSON to run parity test")
	}
	resourcesDir = resolveParityPath(resourcesDir)
	modelDir = resolveParityPath(modelDir)
	expectedPath = resolveParityPath(expectedPath)

	inputPath := filepath.Join("..", "..", "testdata", "parity", "addresses.csv")
	samples, err := readers.ReadDelimited(inputPath, ',', readers.DefaultAddressColumn)
	if err != nil {
		t.Fatalf("read parity input: %v", err)
	}
	expected := readParityExpected(t, expectedPath)
	if len(expected) != len(samples) {
		t.Fatalf("expected fixture length = %d, input samples = %d", len(expected), len(samples))
	}

	cfg := config.Default()
	cfg.Database.PrefixFolderPath = resourcesDir
	cfg.CRF.ModelPath = filepath.Join(modelDir, "address_transformer.onnx")
	cfg.CRF.ModelConfigPath = filepath.Join(modelDir, "address_transformer.config.json")
	cfg.CRF.CRFConfigPath = filepath.Join(modelDir, "address_crf.json")

	db, err := isoruntime.LoadDatabase(cfg)
	if err != nil {
		t.Fatalf("load runtime database: %v", err)
	}
	modelRunner, engine, err := isoruntime.LoadModelRunner(cfg)
	if err != nil {
		t.Fatalf("load model runner: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close model runner: %v", err)
		}
	}()

	results, err := New(cfg, &db, modelRunner).Run(context.Background(), samples)
	if err != nil {
		t.Fatalf("run parity pipeline: %v", err)
	}
	if len(results) != len(expected) {
		t.Fatalf("pipeline results = %d, expected = %d", len(results), len(expected))
	}

	for i := range expected {
		if expected[i].Address != "" && strings.TrimSpace(samples[i].Text) != expected[i].Address {
			t.Fatalf("sample %d address = %q, expected fixture address %q", i, samples[i].Text, expected[i].Address)
		}

		gotCountry := bestMatchOrigin(results[i].FuzzyResult.CountryMatches)
		if gotCountry != expected[i].Country {
			t.Fatalf("sample %d top country = %q, want %q; matches=%#v", i, gotCountry, expected[i].Country, results[i].FuzzyResult.CountryMatches)
		}

		gotTown := bestMatchPossibility(results[i].FuzzyResult.TownMatches)
		if gotTown != expected[i].Town {
			t.Fatalf("sample %d top town = %q, want %q; matches=%#v", i, gotTown, expected[i].Town, results[i].FuzzyResult.TownMatches)
		}
	}
}

func resolveParityPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return filepath.Join("..", "..", path)
}

func readParityExpected(t *testing.T, path string) []parityExpected {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read expected parity fixture: %v", err)
	}
	var expected []parityExpected
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatalf("decode expected parity fixture: %v", err)
	}
	return expected
}

func bestMatchOrigin(matches []core.FuzzyMatch) string {
	match, ok := bestFuzzyMatch(matches)
	if !ok {
		return ""
	}
	return match.Origin
}

func bestMatchPossibility(matches []core.FuzzyMatch) string {
	match, ok := bestFuzzyMatch(matches)
	if !ok {
		return ""
	}
	return match.Possibility
}

func bestFuzzyMatch(matches []core.FuzzyMatch) (core.FuzzyMatch, bool) {
	if len(matches) == 0 {
		return core.FuzzyMatch{}, false
	}
	best := matches[0]
	for _, match := range matches[1:] {
		if match.FinalScore > best.FinalScore {
			best = match
		}
	}
	return best, true
}
