package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tipmarket/swift-ai/internal/config"
	"github.com/tipmarket/swift-ai/internal/core"
	"github.com/tipmarket/swift-ai/internal/model"
	"github.com/tipmarket/swift-ai/internal/resources"
)

type ConvertedModelConfig struct {
	Vocabulary         []string        `json:"vocabulary"`
	BIOTagsToKeep      json.RawMessage `json:"bio_tags_to_keep"`
	TagsToKeep         json.RawMessage `json:"tags_to_keep"`
	IDToCountry        map[int]string  `json:"id_to_country"`
	StrictBeforeInside bool            `json:"strict_before_inside"`
	InputNames         []string        `json:"input_names"`
	OutputNames        []string        `json:"output_names"`
	TagCount           int             `json:"tag_count"`
	MaxSequenceLength  int             `json:"max_sequence_length"`
}

type ConvertedCRFConfig struct {
	Start             []float64   `json:"start"`
	End               []float64   `json:"end"`
	StartTransitions  []float64   `json:"start_transitions"`
	EndTransitions    []float64   `json:"end_transitions"`
	Transitions       [][]float64 `json:"transitions"`
	TransitionsOrder2 [][]float64 `json:"transitions_order_2"`
}

func LoadDatabase(cfg config.Config) (resources.Database, error) {
	dbPath := func(path string) string {
		return ResourcePath(cfg.Database.PrefixFolderPath, path)
	}

	countryAlpha2, countryPossibilities, err := resources.LoadCountryAliases(
		dbPath(cfg.Database.CountryAliases),
		dbPath(cfg.Database.CountryProvinceAliases),
	)
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country aliases: %w", err)
	}

	townAliases, err := resources.LoadCompressedJSON[map[string][]string](dbPath(cfg.Database.TownAliases))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load town aliases: %w", err)
	}

	townPossibilities, townPopulations, largestTownCountry, err := resources.LoadTownsFromParquet(dbPath(cfg.Database.GeonamesParquet), townAliases)
	if err != nil {
		return resources.Database{}, fmt.Errorf("load towns: %w", err)
	}

	countryTownSameName, err := resources.LoadJSON[map[string]string](dbPath(cfg.Database.CountryTownSameName))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country-town same-name data: %w", err)
	}
	countryGroupings, err := resources.LoadJSON[map[string][]string](dbPath(cfg.Database.CountryGroupings))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country groupings: %w", err)
	}
	countrySpecs, err := resources.LoadCompressedJSON[map[string]resources.CountrySpec](dbPath(cfg.Database.CountrySpecs))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load country specs: %w", err)
	}
	provinces, err := resources.LoadCompressedJSON[map[string][]string](dbPath(cfg.Database.CountryProvinceAliases))
	if err != nil {
		return resources.Database{}, fmt.Errorf("load provinces: %w", err)
	}

	return resources.Database{
		CountryAlpha2:        countryAlpha2,
		CountryPossibilities: countryPossibilities,
		TownPossibilities:    townPossibilities,
		TownPopulations:      townPopulations,
		LargestTownCountry:   largestTownCountry,
		CountryTownSameName:  countryTownSameName,
		CountryGroupings:     countryGroupings,
		CountrySpecs:         countrySpecs,
		Provinces:            provinces,
	}, nil
}

func ResourcePath(prefix string, path string) string {
	if filepath.IsAbs(path) || prefix == "" {
		return path
	}
	return filepath.Join(prefix, path)
}

func LoadModelRunner(cfg config.Config) (*model.Runner, *model.ONNXEngine, error) {
	modelCfg, err := LoadModelConfig(cfg.CRF.ModelConfigPath)
	if err != nil {
		return nil, nil, err
	}
	bioTags, err := DecodeBIOTags(modelCfg.BIOTagsToKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("decode bio_tags_to_keep: %w", err)
	}
	tags, err := DecodeTags(modelCfg.TagsToKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("decode tags_to_keep: %w", err)
	}

	crfCfg, err := LoadCRFConfig(cfg.CRF.CRFConfigPath)
	if err != nil {
		return nil, nil, err
	}
	tokenizer, err := model.NewCharacterTokenizer(modelCfg.Vocabulary)
	if err != nil {
		return nil, nil, fmt.Errorf("create tokenizer: %w", err)
	}

	maxSequenceLength := cfg.CRF.MaxSequenceLength
	if modelCfg.MaxSequenceLength > 0 {
		maxSequenceLength = modelCfg.MaxSequenceLength
	}
	tagCount := modelCfg.TagCount
	if tagCount == 0 {
		tagCount = len(bioTags)
	}

	engine, err := model.NewONNXInferenceEngine(model.ONNXConfig{
		ModelPath:         cfg.CRF.ModelPath,
		SharedLibraryPath: os.Getenv("ISO20022_ONNX_RUNTIME"),
		InputNames:        modelCfg.InputNames,
		OutputNames:       modelCfg.OutputNames,
		TagCount:          tagCount,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ONNX inference engine: %w", err)
	}

	runner, err := model.NewRunner(tokenizer, engine, crfCfg, model.RunnerConfig{
		MaxSequenceLength:  maxSequenceLength,
		BIOTagsToKeep:      bioTags,
		TagsToKeep:         tags,
		IDToCountry:        modelCfg.IDToCountry,
		StrictBeforeInside: modelCfg.StrictBeforeInside,
	})
	if err != nil {
		_ = engine.Close()
		return nil, nil, fmt.Errorf("create model runner: %w", err)
	}

	return runner, engine, nil
}

func LoadModelConfig(path string) (ConvertedModelConfig, error) {
	var cfg ConvertedModelConfig
	if err := loadJSON(path, &cfg); err != nil {
		return ConvertedModelConfig{}, fmt.Errorf("load model config %q: %w", path, err)
	}
	return cfg, nil
}

func LoadCRFConfig(path string) (model.CRF, error) {
	var cfg ConvertedCRFConfig
	if err := loadJSON(path, &cfg); err != nil {
		return model.CRF{}, fmt.Errorf("load CRF config %q: %w", path, err)
	}
	start := cfg.Start
	if len(start) == 0 {
		start = cfg.StartTransitions
	}
	end := cfg.End
	if len(end) == 0 {
		end = cfg.EndTransitions
	}
	return model.CRF{
		Start:             start,
		End:               end,
		Transitions:       cfg.Transitions,
		TransitionsOrder2: cfg.TransitionsOrder2,
	}, nil
}

func DecodeBIOTags(raw json.RawMessage) ([]core.BIOTag, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing bio_tags_to_keep")
	}

	var tags []core.BIOTag
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags, nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	tags = make([]core.BIOTag, len(names))
	for i, name := range names {
		tag, err := parseBIOTag(name)
		if err != nil {
			return nil, fmt.Errorf("tag %d: %w", i, err)
		}
		tags[i] = tag
	}
	return tags, nil
}

func DecodeTags(raw json.RawMessage) ([]core.Tag, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing tags_to_keep")
	}

	var tags []core.Tag
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags, nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	tags = make([]core.Tag, len(names))
	for i, name := range names {
		tags[i] = core.Tag(name)
	}
	return tags, nil
}

func loadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	return nil
}

func parseBIOTag(name string) (core.BIOTag, error) {
	name = strings.TrimSpace(name)
	if name == core.BioOther.String() {
		return core.BIOTag{BIO: core.BioOther, Tag: core.TagOther}, nil
	}
	for _, bio := range []core.BIO{core.BioBefore, core.BioInside} {
		if strings.HasPrefix(name, bio.String()) {
			tagName := strings.TrimPrefix(name, bio.String())
			if tagName == "" {
				return core.BIOTag{}, fmt.Errorf("empty tag in %q", name)
			}
			return core.BIOTag{BIO: bio, Tag: core.Tag(tagName)}, nil
		}
	}
	if _, err := strconv.Atoi(name); err == nil {
		return core.BIOTag{}, fmt.Errorf("numeric BIO tag %q is unsupported", name)
	}
	return core.BIOTag{}, fmt.Errorf("unsupported BIO tag %q", name)
}
