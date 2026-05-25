# ISO 20022 Address Structuring Go Port Design

## Goal

Port `Swift-SC/iso20022-address-structuring` from Python to Go as a native Go library and CLI. The Go runtime must not shell out to Python for normal inference. It will use ONNX Runtime for the neural Transformer/country-head model and native Go code for the rest of the pipeline, including CRF decoding, fuzzy matching, postcode matching, post-processing, and output formatting.

## Source Project Summary

The upstream Python project reads unstructured postal addresses and extracts town and country candidates for ISO 20022 CBPR+ address structuring. Its pipeline is:

1. Read address samples from text, CSV, TSV, or DataFrame-like sources.
2. Clean and validate each sample.
3. Run a Transformer + second-order CRF model to tag address spans and infer a likely country.
4. Fuzzy-match countries, country codes, towns, and optional extended towns.
5. Match postcodes against preprocessed GeoNames-derived postcode resources.
6. Add flags, compute scores, combine country/town matches, and write CSV/TSV/JSON output.

The Python package depends on PyTorch, safetensors, polars, RapidFuzz, fuzzysearch, anyascii, jellyfish, pydantic, orjson, and fastparquet. The Go port will replace these with Go-native packages where practical and ONNX Runtime for neural model execution.

## Architecture

The Go module will use this structure:

```text
cmd/iso-run/                 CLI entry point equivalent to iso_run
internal/config/             Typed configuration and environment/flag parsing
internal/pipeline/           AddressStructuringPipeline orchestration
internal/readers/            Text, CSV, and TSV readers
internal/normalize/          ASCII transliteration and alias variant generation
internal/resources/          Compressed JSON and town/postcode resource loading
internal/model/              Tokenizer, ONNX session, CRF decode/marginals, span creation
internal/fuzzy/              Fuzzy scan and FuzzyMatch result types
internal/postcode/           Postcode regex matching
internal/postprocess/        Flags, score computer, combination generator, final results
internal/output/             Human-readable CSV/TSV and JSON writers
tools/convert-model/         Python-assisted one-time model export scripts and docs
```

The runtime data flow will be:

```text
Reader -> clean/validate -> ONNX emissions + country head -> Go CRF
       -> fuzzy matching -> postcode matching -> post-processing -> output
```

The model path is intentionally split. ONNX Runtime will run the Transformer encoder and country head. Go will implement CRF Viterbi decoding and CRF marginal probabilities using exported transition tensors. This avoids embedding Python control flow into ONNX while preserving Python behavior closely.

## Public API

The Go library will expose a small public package once the internals are stable:

```go
type AddressSample struct {
	Text                  string
	SuggestedCountry      string
	HasSuggestedCountry   bool
	ForceSuggestedCountry bool
}

type Pipeline struct {
	// constructed with Config and loaded resources
}

func NewPipeline(ctx context.Context, cfg Config) (*Pipeline, error)
func (p *Pipeline) Run(ctx context.Context, samples []AddressSample) ([]Result, error)
```

The CLI will support:

```bash
iso-run --input-path input.csv --output-path output.csv --batch-size 1024 --verbose
iso-run -i input.tsv -o output.json
```

Supported input formats are `.txt`, `.csv`, and `.tsv`. Supported output formats are `.csv`, `.tsv`, and `.json`.

## Model Artifacts

The Go port will use a converted model bundle:

```text
resources/models/address_transformer.onnx
resources/models/address_transformer.config.json
resources/models/address_crf.json
```

`address_transformer.onnx` will accept padded character token IDs and a boolean mask. It will output token emissions and country logits. Go will apply softmax and top-1 selection for the country-head result.

`address_transformer.config.json` will include vocabulary, padding index, max sequence length, BIO tag order, entity tag order, country ID mapping, embedding settings for auditability, and output tensor names.

`address_crf.json` will include:

- `start_transitions`
- `end_transitions`
- `transitions`
- `transitions_order_2`

The conversion tool is allowed to use Python, PyTorch, and safetensors during development. The generated Go runtime bundle must be usable without Python.

## Resource Compatibility

The Go loader will first target the existing Swift resource layout:

```text
resources/
  towns_all_countries.parquet
  town_aliases.json
  country_names.json
  country_province_names.json
  post_codes/*.json
  misc/*.json
  models/*
```

Compressed JSON resources produced by the Python preprocessors will be read with Go `compress/zlib` and `encoding/json`.

Town data will be read directly from `towns_all_countries.parquet` using a Go parquet library. The model/resource conversion tooling can also emit a derived compressed JSON town index for debugging and fallback, but the primary runtime path is direct parquet loading so the Go CLI accepts the same resource layout as upstream.

OSM extended towns will be optional and disabled by default, matching upstream defaults. If enabled, the Go port will read the configured OSM parquet file and apply the same filters for labels, place types, population, country overrides, duplicate alias generation, and one-edit-distance exclusion.

## Core Component Behavior

Normalization will mirror Python’s `decode_and_clean_str`, separator alias generation, and Saint/St variations. Transliteration will be isolated behind a small helper so parity tests can pin the upstream examples and any package-specific differences.

The tokenizer will mirror the Python character tokenizer:

- Unknown token index first.
- Padding token index second.
- Configured vocabulary after the two special tokens.
- Unknown characters map to the unknown token.

CRF behavior will mirror Python:

- Viterbi decode over emissions plus summed first-order and second-order transition matrix.
- Marginal probabilities through forward/backward log-sum-exp.
- Entity log-probability and emission vectors grouped by entity tag.
- BIO-to-span grouping in non-strict mode for inference parity.

Fuzzy matching will follow upstream semantics:

- Candidate filtering with a partial-ratio style score cutoff.
- Near-match scanning with max Levenshtein distance.
- Exact matching for two-character country codes and short aliases.
- Inside-word, newline-distance, and origin expansion behavior.

Postcode matching will process text by replacing non-alphanumeric characters with spaces, then apply the same country-specific postcode structures for Argentina, Brazil, Chile, China, Ireland, and Malta plus the general dataset.

Post-processing will port the Python logic for:

- CRF score assignment from average marginal probabilities and emissions.
- Suggested country soft/hard behavior.
- Town and country flags.
- Country/town relationship flags.
- Postcode-town flags.
- Final town/country scores.
- Combination generation, thresholding, sorting, and deduplication.

## Testing Strategy

The port will use TDD for implementation. Initial tests will be Go equivalents of upstream unit tests:

- Normalization.
- Reader behavior.
- Score computation.
- Combination generation.
- Flag managers.
- Postcode matching.
- Fuzzy scan behavior on focused fixtures.
- CRF Viterbi and marginal probability fixtures.

Parity tests will compare Go output against the Python package for fixed small fixtures. When the model and resources are available, golden tests will compare:

- ONNX emissions against PyTorch emissions within tolerance.
- Country predictions against Python country head output.
- CRF decoded spans against Python decoded spans.
- Final top country/town output against Python output for sample addresses.

`go test ./...` will be the required verification command. Model/resource-heavy tests will be gated behind an environment variable such as `ISO20022_RESOURCES_DIR` so normal unit tests can run quickly.

## Error Handling

The CLI will return explicit errors for:

- Unsupported input or output suffix.
- Missing address column.
- Missing or unreadable resource files.
- Input addresses longer than configured max sequence length.
- ONNX Runtime initialization failure.
- Model config/artifact mismatch.

Library methods will return wrapped Go errors with context. The CLI will log structured errors with `log/slog`.

## Scope Boundaries

The first full-port milestone includes inference, CLI, resource loading, model conversion tooling, deterministic post-processing, and tests. It explicitly excludes model training, notebooks, Python package publishing, and Connect-RPC service wrapping.

## Risks And Mitigations

ONNX export may not directly support every PyTorch module detail. The mitigation is to export only the Transformer and country head, then implement CRF decode and marginals in Go.

Fuzzy matching libraries differ across languages. The mitigation is to build small parity fixtures and keep the matching API isolated so candidate filtering can be tuned without changing post-processing.

Parquet support may introduce dependency or compatibility friction. The mitigation is to verify direct parquet loading in the first resource-loading task and keep the derived JSON emitter as a debugging tool, not as the default runtime contract.

Numerical parity will not be bit-for-bit across PyTorch and ONNX Runtime. The mitigation is tolerance-based tests at the emissions/country-head boundary and exact tests for deterministic Go CRF/post-processing behavior.

## Acceptance Criteria

The port is complete when:

1. `go test ./...` passes.
2. `go build ./cmd/iso-run` succeeds.
3. The CLI reads `.txt`, `.csv`, and `.tsv` inputs.
4. The CLI writes `.csv`, `.tsv`, and `.json` outputs.
5. The ONNX model bundle runs without Python at inference time.
6. Unit tests cover the deterministic modules ported from Python.
7. Golden parity tests pass for representative addresses when resources are available.
8. Documentation explains model conversion, resource layout, CLI usage, and test commands.
