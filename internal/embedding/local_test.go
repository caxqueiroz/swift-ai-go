package embedding

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

func TestBasicTokensLowercasesStripsAccentsAndSplitsPunctuation(t *testing.T) {
	got := basicTokens("77 Rue de l'Université, Paris")
	want := []string{"77", "rue", "de", "l", "'", "universite", ",", "paris"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("basicTokens() = %#v, want %#v", got, want)
	}
}

func TestWordPieceTokenizerEncodesAndPads(t *testing.T) {
	tokenizer := testTokenizer(t)

	got := tokenizer.encode("Rue Universite", 8)
	wantIDs := []int64{2, 5, 6, 7, 3, 0, 0, 0}
	wantMask := []int64{1, 1, 1, 1, 1, 0, 0, 0}
	if !reflect.DeepEqual(got.inputIDs, wantIDs) {
		t.Fatalf("inputIDs = %#v, want %#v", got.inputIDs, wantIDs)
	}
	if !reflect.DeepEqual(got.attentionMask, wantMask) {
		t.Fatalf("attentionMask = %#v, want %#v", got.attentionMask, wantMask)
	}
}

func TestMeanPoolNormalizesNonPaddingTokens(t *testing.T) {
	data := []float64{
		1, 0,
		0, 1,
		10, 10,
	}
	vector, err := meanPool(data, ort.NewShape(1, 3, 2), []int64{1, 1, 0})
	if err != nil {
		t.Fatalf("meanPool() error = %v", err)
	}
	normalize(vector)

	want := 1 / math.Sqrt2
	if math.Abs(vector[0]-want) > 1e-9 || math.Abs(vector[1]-want) > 1e-9 {
		t.Fatalf("normalized vector = %#v, want both %f", vector, want)
	}
}

func TestLoadWordPieceTokenizerRequiresSpecialTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vocab.txt")
	if err := os.WriteFile(path, []byte("[PAD]\n[UNK]\n[CLS]\n"), 0o600); err != nil {
		t.Fatalf("write vocab: %v", err)
	}
	_, err := loadWordPieceTokenizer(path)
	if err == nil {
		t.Fatal("loadWordPieceTokenizer() error = nil, want missing [SEP]")
	}
}

func TestLocalONNXIntegration(t *testing.T) {
	modelPath := os.Getenv("ISO20022_EMBEDDING_ONNX_MODEL")
	vocabPath := os.Getenv("ISO20022_EMBEDDING_VOCAB")
	if modelPath == "" {
		modelPath = DefaultLocalModelPath
	}
	if vocabPath == "" {
		vocabPath = DefaultLocalVocabPath
	}
	modelPath = testRepoPath(t, modelPath)
	vocabPath = testRepoPath(t, vocabPath)
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("local embedding model unavailable: %v", err)
	}
	if _, err := os.Stat(vocabPath); err != nil {
		t.Skipf("local embedding vocab unavailable: %v", err)
	}

	embedder, err := NewLocalONNX(LocalConfig{
		ModelPath:         modelPath,
		VocabPath:         vocabPath,
		SharedLibraryPath: os.Getenv("ISO20022_ONNX_RUNTIME"),
	})
	if err != nil {
		t.Skipf("ONNX Runtime unavailable: %v", err)
	}
	defer func() {
		if err := embedder.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	vector, err := embedder.Embed(context.Background(), "77 RUE DE RIVOLI 75001 PARIS")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vector) != 384 {
		t.Fatalf("embedding dimensions = %d, want 384", len(vector))
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-5 {
		t.Fatalf("embedding norm = %.8f, want normalized vector", math.Sqrt(norm))
	}
}

func testRepoPath(t *testing.T, path string) string {
	t.Helper()

	if filepath.IsAbs(path) {
		return path
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", path)
}

func testTokenizer(t *testing.T) wordPieceTokenizer {
	t.Helper()

	vocab := map[string]int64{
		"[PAD]":      0,
		"[UNK]":      1,
		"[CLS]":      2,
		"[SEP]":      3,
		"r":          4,
		"rue":        5,
		"un":         6,
		"##iversite": 7,
	}
	return wordPieceTokenizer{
		vocab: vocab,
		padID: vocab["[PAD]"],
		unkID: vocab["[UNK]"],
		clsID: vocab["[CLS]"],
		sepID: vocab["[SEP]"],
	}
}
