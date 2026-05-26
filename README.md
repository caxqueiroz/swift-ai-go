# ISO 20022 Address Structuring Go Port

This repository is a Go port of `Swift-SC/iso20022-address-structuring`. It contains the address cleaning, fuzzy matching, CRF decoding, post-processing, output writers, and an `iso-run` CLI wired to ONNX Runtime.

The upstream resource files and trained model weights are not vendored here. Use the original resource directory and convert the PyTorch checkpoint before running full inference.

## Layout

- `cmd/iso-run`: CLI entrypoint.
- `internal/pipeline`: end-to-end orchestration.
- `internal/model`: character tokenizer, ONNX adapter, CRF decoding, and span grouping.
- `internal/fuzzy`, `internal/postcode`, `internal/postprocess`: matching and final scoring.
- `internal/resources`: resource file loading.
- `tools/convert-model`: PyTorch-to-ONNX conversion script.
- `testdata/parity`: small gated parity fixtures.

## Build And Test

```bash
go test ./...
go build ./cmd/iso-run
```

`internal/model` uses `github.com/yalue/onnxruntime_go`. For real inference, install ONNX Runtime and set `ISO20022_ONNX_RUNTIME` if the shared library is not discoverable by default.

## Convert The Model

```bash
python3 tools/convert-model/export_onnx.py \
  --source-root /path/to/iso20022-address-structuring \
  --weights /path/to/resources/models/CRF_with_MLP_EPOCH_1.safetensors \
  --config /path/to/resources/models/CRF_with_MLP_EPOCH_1.config.json \
  --output-dir resources/models
```

This writes:

- `resources/models/address_transformer.onnx`
- `resources/models/address_transformer.config.json`
- `resources/models/address_crf.json`

## Run The CLI

```bash
ISO20022_ONNX_RUNTIME=/path/to/libonnxruntime.so \
go run ./cmd/iso-run \
  --input-path testdata/parity/addresses.csv \
  --output-path /tmp/iso20022-output.json \
  --resources-dir /path/to/upstream/resources \
  --model-dir resources/models
```

Input supports `.txt`, `.csv`, and `.tsv`. Delimited input expects an `address` column and may include `suggested_country` and `force_suggested_country`. Output supports `.csv`, `.tsv`, and `.json`; pass `--verbose` to include CRF emissions and log probabilities in JSON output.

## Parity Check

The parity test is gated because it needs upstream resources, a converted ONNX model, and Python-produced expected output.

```bash
ISO20022_RESOURCES_DIR=/path/to/upstream/resources \
ISO20022_MODEL_DIR=resources/models \
ISO20022_EXPECTED_PARITY_JSON=testdata/parity/expected_python_output.json \
go test ./internal/pipeline -run TestParityAgainstPythonFixtures -count=1
```

To refresh expected results, run the upstream Python CLI against `testdata/parity/addresses.csv`, then map each row to the expected top `country` and `town` fields in `testdata/parity/expected_python_output.json`.
