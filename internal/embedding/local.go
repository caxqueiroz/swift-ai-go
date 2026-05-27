package embedding

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/mozillazg/go-unidecode"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	DefaultLocalMaxSequenceLength = 256
	DefaultLocalModelPath         = "resources/embeddings/all-MiniLM-L6-v2/model.onnx"
	DefaultLocalVocabPath         = "resources/embeddings/all-MiniLM-L6-v2/vocab.txt"
)

var (
	DefaultLocalInputNames  = []string{"input_ids", "attention_mask", "token_type_ids"}
	DefaultLocalOutputNames = []string{"last_hidden_state"}
)

var localONNXEnvState = struct {
	sync.Mutex
	initialized bool
}{}

type LocalConfig struct {
	ModelPath         string
	VocabPath         string
	SharedLibraryPath string
	InputNames        []string
	OutputNames       []string
	MaxSequenceLength int
}

type LocalONNX struct {
	session           *ort.DynamicAdvancedSession
	tokenizer         wordPieceTokenizer
	inputNames        []string
	outputNames       []string
	maxSequenceLength int
}

func NewLocalONNX(cfg LocalConfig) (*LocalONNX, error) {
	modelPath := strings.TrimSpace(cfg.ModelPath)
	if modelPath == "" {
		modelPath = DefaultLocalModelPath
	}
	vocabPath := strings.TrimSpace(cfg.VocabPath)
	if vocabPath == "" {
		vocabPath = DefaultLocalVocabPath
	}
	maxSequenceLength := cfg.MaxSequenceLength
	if maxSequenceLength <= 0 {
		maxSequenceLength = DefaultLocalMaxSequenceLength
	}
	inputNames := cfg.InputNames
	if len(inputNames) == 0 {
		inputNames = DefaultLocalInputNames
	}
	if len(inputNames) != 2 && len(inputNames) != 3 {
		return nil, fmt.Errorf("local embedding ONNX input names length = %d, want 2 or 3", len(inputNames))
	}
	outputNames := cfg.OutputNames
	if len(outputNames) == 0 {
		outputNames = DefaultLocalOutputNames
	}
	if len(outputNames) != 1 {
		return nil, fmt.Errorf("local embedding ONNX output names length = %d, want 1", len(outputNames))
	}

	tokenizer, err := loadWordPieceTokenizer(vocabPath)
	if err != nil {
		return nil, err
	}
	if err := initializeLocalONNX(cfg.SharedLibraryPath); err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("creating local embedding ONNX session: %w", err)
	}
	return &LocalONNX{
		session:           session,
		tokenizer:         tokenizer,
		inputNames:        append([]string(nil), inputNames...),
		outputNames:       append([]string(nil), outputNames...),
		maxSequenceLength: maxSequenceLength,
	}, nil
}

func initializeLocalONNX(sharedLibraryPath string) error {
	localONNXEnvState.Lock()
	defer localONNXEnvState.Unlock()

	if ort.IsInitialized() {
		localONNXEnvState.initialized = true
		return nil
	}
	if sharedLibraryPath != "" {
		ort.SetSharedLibraryPath(sharedLibraryPath)
	}
	if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		return fmt.Errorf("initializing ONNX Runtime for local embeddings: %w", err)
	}
	localONNXEnvState.initialized = true
	return nil
}

func (e *LocalONNX) Close() error {
	if e != nil && e.session != nil {
		err := e.session.Destroy()
		e.session = nil
		if err != nil {
			return fmt.Errorf("destroying local embedding ONNX session: %w", err)
		}
	}
	return nil
}

func (e *LocalONNX) Embed(ctx context.Context, text string) ([]float64, error) {
	if e == nil || e.session == nil {
		return nil, errors.New("local embedding session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("before local embedding inference: %w", err)
	}

	encoded := e.tokenizer.encode(text, e.maxSequenceLength)
	shape := ort.NewShape(1, int64(e.maxSequenceLength))
	inputIDTensor, err := ort.NewTensor(shape, encoded.inputIDs)
	if err != nil {
		return nil, fmt.Errorf("creating embedding input_ids tensor: %w", err)
	}
	defer destroyValue(inputIDTensor)
	attentionMaskTensor, err := ort.NewTensor(shape, encoded.attentionMask)
	if err != nil {
		return nil, fmt.Errorf("creating embedding attention_mask tensor: %w", err)
	}
	defer destroyValue(attentionMaskTensor)

	inputs := []ort.Value{inputIDTensor, attentionMaskTensor}
	if len(e.inputNames) == 3 {
		tokenTypeTensor, err := ort.NewTensor(shape, encoded.tokenTypeIDs)
		if err != nil {
			return nil, fmt.Errorf("creating embedding token_type_ids tensor: %w", err)
		}
		defer destroyValue(tokenTypeTensor)
		inputs = append(inputs, tokenTypeTensor)
	}

	outputs := make([]ort.Value, len(e.outputNames))
	defer destroyValues(outputs)
	if err := e.session.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("running local embedding ONNX session: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("after local embedding inference: %w", err)
	}

	data, shapeOut, err := floatTensorData(outputs[0])
	if err != nil {
		return nil, fmt.Errorf("reading local embedding output: %w", err)
	}
	vector, err := meanPool(data, shapeOut, encoded.attentionMask)
	if err != nil {
		return nil, err
	}
	normalize(vector)
	return vector, nil
}

type encodedText struct {
	inputIDs      []int64
	attentionMask []int64
	tokenTypeIDs  []int64
}

type wordPieceTokenizer struct {
	vocab map[string]int64
	unkID int64
	clsID int64
	sepID int64
	padID int64
}

func loadWordPieceTokenizer(path string) (wordPieceTokenizer, error) {
	file, err := os.Open(path)
	if err != nil {
		return wordPieceTokenizer{}, fmt.Errorf("open embedding vocab %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	vocab := map[string]int64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		token := strings.TrimSpace(scanner.Text())
		if token == "" {
			continue
		}
		vocab[token] = int64(len(vocab))
	}
	if err := scanner.Err(); err != nil {
		return wordPieceTokenizer{}, fmt.Errorf("read embedding vocab %q: %w", path, err)
	}

	tokenizer := wordPieceTokenizer{vocab: vocab}
	var ok bool
	if tokenizer.unkID, ok = vocab["[UNK]"]; !ok {
		return wordPieceTokenizer{}, errors.New("embedding vocab missing [UNK]")
	}
	if tokenizer.clsID, ok = vocab["[CLS]"]; !ok {
		return wordPieceTokenizer{}, errors.New("embedding vocab missing [CLS]")
	}
	if tokenizer.sepID, ok = vocab["[SEP]"]; !ok {
		return wordPieceTokenizer{}, errors.New("embedding vocab missing [SEP]")
	}
	if tokenizer.padID, ok = vocab["[PAD]"]; !ok {
		return wordPieceTokenizer{}, errors.New("embedding vocab missing [PAD]")
	}
	return tokenizer, nil
}

func (t wordPieceTokenizer) encode(text string, maxSequenceLength int) encodedText {
	pieceIDs := make([]int64, 0, maxSequenceLength)
	pieceIDs = append(pieceIDs, t.clsID)
	for _, token := range basicTokens(text) {
		pieces := t.wordPieces(token)
		for _, id := range pieces {
			if len(pieceIDs) >= maxSequenceLength-1 {
				break
			}
			pieceIDs = append(pieceIDs, id)
		}
		if len(pieceIDs) >= maxSequenceLength-1 {
			break
		}
	}
	pieceIDs = append(pieceIDs, t.sepID)

	inputIDs := make([]int64, maxSequenceLength)
	attentionMask := make([]int64, maxSequenceLength)
	tokenTypeIDs := make([]int64, maxSequenceLength)
	for i := range inputIDs {
		inputIDs[i] = t.padID
	}
	for i, id := range pieceIDs {
		inputIDs[i] = id
		attentionMask[i] = 1
	}
	return encodedText{inputIDs: inputIDs, attentionMask: attentionMask, tokenTypeIDs: tokenTypeIDs}
}

func (t wordPieceTokenizer) wordPieces(token string) []int64 {
	if token == "" {
		return nil
	}
	if id, ok := t.vocab[token]; ok {
		return []int64{id}
	}

	runes := []rune(token)
	if len(runes) > 100 {
		return []int64{t.unkID}
	}
	pieces := make([]int64, 0, len(runes))
	for start := 0; start < len(runes); {
		found := false
		for end := len(runes); end > start; end-- {
			part := string(runes[start:end])
			if start > 0 {
				part = "##" + part
			}
			if id, ok := t.vocab[part]; ok {
				pieces = append(pieces, id)
				start = end
				found = true
				break
			}
		}
		if !found {
			return []int64{t.unkID}
		}
	}
	return pieces
}

func basicTokens(text string) []string {
	text = strings.ToLower(unidecode.Unidecode(text))
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			flush()
			tokens = append(tokens, string(r))
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func meanPool(data []float64, shape ort.Shape, attentionMask []int64) ([]float64, error) {
	if len(shape) != 3 {
		return nil, fmt.Errorf("local embedding output shape = %v, want 3 dimensions", shape)
	}
	batch, seqLen, width, err := checked3DShape(shape, "local embedding output")
	if err != nil {
		return nil, err
	}
	if batch != 1 {
		return nil, fmt.Errorf("local embedding output batch = %d, want 1", batch)
	}
	if len(attentionMask) != seqLen {
		return nil, fmt.Errorf("attention mask length %d does not match output sequence length %d", len(attentionMask), seqLen)
	}
	if len(data) != batch*seqLen*width {
		return nil, fmt.Errorf("local embedding output data length %d does not match shape %v", len(data), shape)
	}

	vector := make([]float64, width)
	var count float64
	for seq := range seqLen {
		if attentionMask[seq] == 0 {
			continue
		}
		offset := seq * width
		for i := range width {
			vector[i] += data[offset+i]
		}
		count++
	}
	if count == 0 {
		return nil, errors.New("local embedding attention mask is empty")
	}
	for i := range vector {
		vector[i] /= count
	}
	return vector, nil
}

func normalize(vector []float64) {
	var sum float64
	for _, value := range vector {
		sum += value * value
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return
	}
	for i := range vector {
		vector[i] /= norm
	}
}

func destroyValues(values []ort.Value) {
	for _, value := range values {
		destroyValue(value)
	}
}

func destroyValue(value ort.Value) {
	if value != nil {
		_ = value.Destroy()
	}
}

func floatTensorData(value ort.Value) ([]float64, ort.Shape, error) {
	if value == nil {
		return nil, nil, errors.New("output tensor is nil")
	}
	switch tensor := value.(type) {
	case *ort.Tensor[float32]:
		data32 := tensor.GetData()
		data := make([]float64, len(data32))
		for i, value := range data32 {
			data[i] = float64(value)
		}
		return data, tensor.GetShape(), nil
	case *ort.Tensor[float64]:
		return append([]float64(nil), tensor.GetData()...), tensor.GetShape(), nil
	default:
		return nil, nil, fmt.Errorf("unsupported output tensor type %T", value)
	}
}

func checked3DShape(shape ort.Shape, name string) (int, int, int, error) {
	first, second, err := checked2DShape(shape[:2], name)
	if err != nil {
		return 0, 0, 0, err
	}
	third, err := checkedDimension(shape[2], name)
	if err != nil {
		return 0, 0, 0, err
	}
	return first, second, third, nil
}

func checked2DShape(shape ort.Shape, name string) (int, int, error) {
	first, err := checkedDimension(shape[0], name)
	if err != nil {
		return 0, 0, err
	}
	second, err := checkedDimension(shape[1], name)
	if err != nil {
		return 0, 0, err
	}
	return first, second, nil
}

func checkedDimension(dim int64, name string) (int, error) {
	if dim < 0 {
		return 0, fmt.Errorf("%s shape has negative dimension %d", name, dim)
	}
	return int(dim), nil
}
