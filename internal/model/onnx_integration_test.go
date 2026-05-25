package model

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestONNXInferenceEngineIntegration(t *testing.T) {
	modelPath := os.Getenv("ISO20022_ONNX_MODEL")
	if modelPath == "" {
		if modelDir := os.Getenv("ISO20022_MODEL_DIR"); modelDir != "" {
			modelPath = filepath.Join(modelDir, "address_transformer.onnx")
		}
	}
	if modelPath == "" {
		t.Skip("set ISO20022_ONNX_MODEL or ISO20022_MODEL_DIR to run ONNX integration test")
	}

	seqLen := 224
	if raw := os.Getenv("ISO20022_ONNX_SEQUENCE_LENGTH"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ISO20022_ONNX_SEQUENCE_LENGTH = %q: %v", raw, err)
		}
		seqLen = parsed
	}

	engine, err := NewONNXInferenceEngine(ONNXConfig{
		ModelPath:          modelPath,
		SharedLibraryPath:  os.Getenv("ISO20022_ONNX_RUNTIME"),
		InputNames:         envCSV("ISO20022_ONNX_INPUTS", DefaultONNXInputNames),
		OutputNames:        envCSV("ISO20022_ONNX_OUTPUTS", DefaultONNXOutputNames),
		DestroyEnvironment: true,
	})
	if err != nil {
		t.Skipf("ONNX runtime unavailable: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	tokenIDs := make([][]int64, 1)
	mask := make([][]bool, 1)
	tokenIDs[0] = make([]int64, seqLen)
	mask[0] = make([]bool, seqLen)
	if seqLen > 0 {
		mask[0][0] = true
	}

	emissions, _, err := engine.Run(context.Background(), tokenIDs, mask)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(emissions) != 1 {
		t.Fatalf("emissions batch size = %d, want 1", len(emissions))
	}
}

func TestValidateONNXOutputDimensionsRejectsMismatches(t *testing.T) {
	tests := []struct {
		name          string
		emissions     [][][]float64
		countryLogits [][]float64
		wantErr       string
	}{
		{
			name:          "emissions batch",
			emissions:     make3D(2, 2, 3),
			countryLogits: make2D(1, 4),
			wantErr:       "emissions batch size 2 does not match input batch size 1",
		},
		{
			name:          "emissions sequence",
			emissions:     make3D(1, 3, 3),
			countryLogits: make2D(1, 4),
			wantErr:       "emissions[0] sequence length 3 does not match input sequence length 2",
		},
		{
			name:          "emissions tags",
			emissions:     make3D(1, 2, 4),
			countryLogits: make2D(1, 4),
			wantErr:       "emissions[0][0] tag count 4 does not match configured tag count 3",
		},
		{
			name:          "country batch",
			emissions:     make3D(1, 2, 3),
			countryLogits: make2D(2, 4),
			wantErr:       "country logits batch size 2 does not match input batch size 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateONNXOutputDimensions(tt.emissions, tt.countryLogits, 1, 2, 3)
			if err == nil {
				t.Fatal("validateONNXOutputDimensions() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateONNXOutputDimensions() error = %q, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateONNXOutputDimensionsAcceptsMatchingOptionalCountryOutput(t *testing.T) {
	if err := validateONNXOutputDimensions(make3D(1, 2, 3), nil, 1, 2, 3); err != nil {
		t.Fatalf("validateONNXOutputDimensions() error = %v", err)
	}
	if err := validateONNXOutputDimensions(make3D(1, 2, 3), make2D(1, 4), 1, 2, 3); err != nil {
		t.Fatalf("validateONNXOutputDimensions() error = %v", err)
	}
}

func envCSV(name string, fallback []string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func make3D(batch int, seqLen int, width int) [][][]float64 {
	out := make([][][]float64, batch)
	for batchIdx := range out {
		out[batchIdx] = make([][]float64, seqLen)
		for seq := range out[batchIdx] {
			out[batchIdx][seq] = make([]float64, width)
		}
	}
	return out
}

func make2D(rows int, cols int) [][]float64 {
	out := make([][]float64, rows)
	for row := range out {
		out[row] = make([]float64, cols)
	}
	return out
}
