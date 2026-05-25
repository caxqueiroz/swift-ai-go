package model

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	DefaultONNXInputNames  = []string{"token_ids", "mask"}
	DefaultONNXOutputNames = []string{"emissions", "country_logits"}
)

var onnxEnvState = struct {
	sync.Mutex
	owned            bool
	active           int
	destroyRequested bool
}{}

type ONNXConfig struct {
	ModelPath          string
	SharedLibraryPath  string
	InputNames         []string
	OutputNames        []string
	TagCount           int
	DestroyEnvironment bool
}

type ONNXEngine struct {
	session            *ort.DynamicAdvancedSession
	outputNames        []string
	tagCount           int
	releaseEnvironment bool
}

func NewONNXInferenceEngine(cfg ONNXConfig) (*ONNXEngine, error) {
	return NewONNXEngine(cfg)
}

func NewONNXEngine(cfg ONNXConfig) (*ONNXEngine, error) {
	if cfg.ModelPath == "" {
		return nil, errors.New("ONNX model path is empty")
	}

	inputNames := cfg.InputNames
	if len(inputNames) == 0 {
		inputNames = DefaultONNXInputNames
	}
	if len(inputNames) != 2 {
		return nil, fmt.Errorf("ONNX input names length = %d, want 2", len(inputNames))
	}

	outputNames := cfg.OutputNames
	if len(outputNames) == 0 {
		outputNames = DefaultONNXOutputNames
	}
	if len(outputNames) == 0 || len(outputNames) > 2 {
		return nil, fmt.Errorf("ONNX output names length = %d, want 1 or 2", len(outputNames))
	}

	releaseOnClose, err := acquireONNXEnvironment(cfg)
	if err != nil {
		return nil, err
	}
	releaseOnFailure := true
	defer func() {
		if releaseOnFailure {
			_ = releaseONNXEnvironment(releaseOnClose)
		}
	}()

	session, err := ort.NewDynamicAdvancedSession(cfg.ModelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("creating ONNX session: %w", err)
	}
	releaseOnFailure = false

	return &ONNXEngine{
		session:            session,
		outputNames:        append([]string(nil), outputNames...),
		tagCount:           cfg.TagCount,
		releaseEnvironment: releaseOnClose,
	}, nil
}

func (e *ONNXEngine) Close() error {
	var closeErr error
	if e.session != nil {
		if err := e.session.Destroy(); err != nil {
			closeErr = fmt.Errorf("destroying ONNX session: %w", err)
		}
		e.session = nil
	}
	if err := releaseONNXEnvironment(e.releaseEnvironment); err != nil {
		if closeErr != nil {
			return fmt.Errorf("%v; %w", closeErr, err)
		}
		return err
	}
	e.releaseEnvironment = false
	return closeErr
}

func (e *ONNXEngine) Run(ctx context.Context, tokenIDs [][]int64, mask [][]bool) ([][][]float64, [][]float64, error) {
	if e.session == nil {
		return nil, nil, errors.New("ONNX session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("before ONNX inference: %w", err)
	}

	flatTokenIDs, batchSize, seqLen, err := flattenTokenIDs(tokenIDs)
	if err != nil {
		return nil, nil, err
	}
	flatMask, err := flattenMask(mask, batchSize, seqLen)
	if err != nil {
		return nil, nil, err
	}

	tokenTensor, err := ort.NewTensor(ort.NewShape(int64(batchSize), int64(seqLen)), flatTokenIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("creating token ID tensor: %w", err)
	}
	defer destroyValue(tokenTensor)

	maskTensor, err := ort.NewTensor(ort.NewShape(int64(batchSize), int64(seqLen)), flatMask)
	if err != nil {
		return nil, nil, fmt.Errorf("creating mask tensor: %w", err)
	}
	defer destroyValue(maskTensor)

	outputs := make([]ort.Value, len(e.outputNames))
	defer destroyValues(outputs)
	if err := e.session.Run([]ort.Value{tokenTensor, maskTensor}, outputs); err != nil {
		return nil, nil, fmt.Errorf("running ONNX session: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("after ONNX inference: %w", err)
	}

	emissionsData, emissionsShape, err := floatTensorData(outputs[0])
	if err != nil {
		return nil, nil, fmt.Errorf("reading ONNX emissions output: %w", err)
	}
	emissions, err := reshape3DFloat64(emissionsData, emissionsShape, "emissions")
	if err != nil {
		return nil, nil, err
	}

	var countryLogits [][]float64
	if len(outputs) > 1 {
		countryData, countryShape, err := floatTensorData(outputs[1])
		if err != nil {
			return nil, nil, fmt.Errorf("reading ONNX country logits output: %w", err)
		}
		countryLogits, err = reshape2DFloat64(countryData, countryShape, "country logits")
		if err != nil {
			return nil, nil, err
		}
	}
	if err := validateONNXOutputDimensions(emissions, countryLogits, batchSize, seqLen, e.tagCount); err != nil {
		return nil, nil, err
	}

	return emissions, countryLogits, nil
}

func acquireONNXEnvironment(cfg ONNXConfig) (releaseOnClose bool, err error) {
	onnxEnvState.Lock()
	defer onnxEnvState.Unlock()

	if !ort.IsInitialized() {
		if cfg.SharedLibraryPath != "" {
			ort.SetSharedLibraryPath(cfg.SharedLibraryPath)
		}
		if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
			return false, fmt.Errorf("initializing ONNX Runtime: %w", err)
		}
		onnxEnvState.owned = true
	}
	if onnxEnvState.owned {
		onnxEnvState.active++
		if cfg.DestroyEnvironment {
			onnxEnvState.destroyRequested = true
		}
		return true, nil
	}
	return false, nil
}

func releaseONNXEnvironment(releaseOnClose bool) error {
	if !releaseOnClose {
		return nil
	}

	onnxEnvState.Lock()
	defer onnxEnvState.Unlock()

	if onnxEnvState.active > 0 {
		onnxEnvState.active--
	}
	if onnxEnvState.active == 0 && onnxEnvState.owned && !ort.IsInitialized() {
		onnxEnvState.owned = false
		onnxEnvState.destroyRequested = false
		return nil
	}
	if onnxEnvState.active != 0 || !onnxEnvState.destroyRequested || !onnxEnvState.owned || !ort.IsInitialized() {
		return nil
	}
	if err := ort.DestroyEnvironment(); err != nil {
		return fmt.Errorf("destroying ONNX environment: %w", err)
	}
	onnxEnvState.owned = false
	onnxEnvState.destroyRequested = false
	return nil
}

func flattenTokenIDs(tokenIDs [][]int64) ([]int64, int, int, error) {
	if len(tokenIDs) == 0 {
		return nil, 0, 0, errors.New("token IDs batch is empty")
	}
	seqLen := len(tokenIDs[0])
	if seqLen == 0 {
		return nil, 0, 0, errors.New("token IDs sequence length is empty")
	}
	flat := make([]int64, 0, len(tokenIDs)*seqLen)
	for batchIdx, row := range tokenIDs {
		if len(row) != seqLen {
			return nil, 0, 0, fmt.Errorf("token IDs row %d length %d does not match row 0 length %d", batchIdx, len(row), seqLen)
		}
		flat = append(flat, row...)
	}
	return flat, len(tokenIDs), seqLen, nil
}

func flattenMask(mask [][]bool, batchSize int, seqLen int) ([]bool, error) {
	if len(mask) != batchSize {
		return nil, fmt.Errorf("mask batch size %d does not match token IDs batch size %d", len(mask), batchSize)
	}
	flat := make([]bool, 0, batchSize*seqLen)
	for batchIdx, row := range mask {
		if len(row) != seqLen {
			return nil, fmt.Errorf("mask row %d length %d does not match token IDs sequence length %d", batchIdx, len(row), seqLen)
		}
		flat = append(flat, row...)
	}
	return flat, nil
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

func reshape3DFloat64(data []float64, shape ort.Shape, name string) ([][][]float64, error) {
	if len(shape) != 3 {
		return nil, fmt.Errorf("%s shape = %v, want 3 dimensions", name, shape)
	}
	batch, seqLen, width, err := checked3DShape(shape, name)
	if err != nil {
		return nil, err
	}
	if len(data) != batch*seqLen*width {
		return nil, fmt.Errorf("%s data length %d does not match shape %v", name, len(data), shape)
	}
	out := make([][][]float64, batch)
	offset := 0
	for batchIdx := range out {
		out[batchIdx] = make([][]float64, seqLen)
		for seq := range out[batchIdx] {
			out[batchIdx][seq] = append([]float64(nil), data[offset:offset+width]...)
			offset += width
		}
	}
	return out, nil
}

func reshape2DFloat64(data []float64, shape ort.Shape, name string) ([][]float64, error) {
	if len(shape) != 2 {
		return nil, fmt.Errorf("%s shape = %v, want 2 dimensions", name, shape)
	}
	rows, cols, err := checked2DShape(shape, name)
	if err != nil {
		return nil, err
	}
	if len(data) != rows*cols {
		return nil, fmt.Errorf("%s data length %d does not match shape %v", name, len(data), shape)
	}
	out := make([][]float64, rows)
	offset := 0
	for row := range out {
		out[row] = append([]float64(nil), data[offset:offset+cols]...)
		offset += cols
	}
	return out, nil
}

func validateONNXOutputDimensions(emissions [][][]float64, countryLogits [][]float64, batchSize int, seqLen int, tagCount int) error {
	if len(emissions) != batchSize {
		return fmt.Errorf("emissions batch size %d does not match input batch size %d", len(emissions), batchSize)
	}
	for batchIdx, batch := range emissions {
		if len(batch) != seqLen {
			return fmt.Errorf("emissions[%d] sequence length %d does not match input sequence length %d", batchIdx, len(batch), seqLen)
		}
		if tagCount <= 0 {
			continue
		}
		for seqIdx, token := range batch {
			if len(token) != tagCount {
				return fmt.Errorf("emissions[%d][%d] tag count %d does not match configured tag count %d", batchIdx, seqIdx, len(token), tagCount)
			}
		}
	}
	if countryLogits != nil && len(countryLogits) != batchSize {
		return fmt.Errorf("country logits batch size %d does not match input batch size %d", len(countryLogits), batchSize)
	}
	return nil
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
