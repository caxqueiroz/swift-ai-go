# ONNX Model Conversion

Use `export_onnx.py` to convert the upstream Swift-SC PyTorch checkpoint into the artifacts consumed by `cmd/iso-run`:

- `address_transformer.onnx`
- `address_transformer.config.json`
- `address_crf.json`

The script expects the upstream Python project and its dependencies to be available. If the upstream package is not installed, pass `--source-root` pointing at a checkout of `Swift-SC/iso20022-address-structuring`.

```bash
python3 tools/convert-model/export_onnx.py \
  --source-root /path/to/iso20022-address-structuring \
  --weights /path/to/resources/models/CRF_with_MLP_EPOCH_1.safetensors \
  --config /path/to/resources/models/CRF_with_MLP_EPOCH_1.config.json \
  --output-dir resources/models
```

The exported ONNX graph has two inputs, `token_ids` and `mask`, and two outputs, `emissions` and `country_logits`. The Go runtime performs CRF decoding itself, so the script writes CRF transition tensors separately in `address_crf.json`.

If the ONNX Runtime shared library is not discoverable at runtime, set `ISO20022_ONNX_RUNTIME` when running `iso-run`.
