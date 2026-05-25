#!/usr/bin/env python3
"""Export the upstream Swift-SC PyTorch model for the Go runtime."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


DEFAULT_INPUT_NAMES = ["token_ids", "mask"]
DEFAULT_OUTPUT_NAMES = ["emissions", "country_logits"]


def main() -> int:
    args = parse_args()
    if args.source_root is not None:
        sys.path.insert(0, str(args.source_root.resolve()))

    try:
        import torch
        import safetensors.torch
        from data_structuring.components.models import TransformerCRF
        from data_structuring.components.tags import BIOTag, Tag
    except ImportError as exc:
        raise SystemExit(
            "missing Python dependencies or upstream package; install the upstream "
            "project and its dependencies, or pass --source-root"
        ) from exc

    with args.config.open("r", encoding="utf-8") as file:
        raw_config = json.load(file)

    mapping_id_to_country = {
        int(country_id): country_code
        for country_id, country_code in raw_config["mapping_id_to_country"].items()
    }
    tags_to_keep = [Tag(tag) for tag in raw_config["tags_to_keep"]]
    bio_tags_to_keep = [BIOTag(**tag) for tag in raw_config["bio_tags_to_keep"]]

    weights = safetensors.torch.load_file(str(args.weights), device=args.device)
    use_country_predictor = any("country_predictor" in name for name in weights)

    model = TransformerCRF(
        vocab_size=len(raw_config["vocabulary"]) + 2,
        tags=bio_tags_to_keep,
        mapping_id_to_country=mapping_id_to_country,
        max_seq_len=raw_config["max_sequence_length"],
        d_model=raw_config["embedding_dimension"],
        nhead=raw_config["n_heads"],
        depth=raw_config["depth"],
        padding_idx=raw_config.get("padding_value", 1),
        use_country_classifier=use_country_predictor,
        regularisation_emissions=raw_config.get("regularisation_emissions", 0),
        regularisation_transitions=raw_config.get("regularisation_transitions", 0),
        regularisation_transitions_order_2=raw_config.get("regularisation_transitions_order_2", 0),
    ).to(args.device)
    model.load_state_dict(weights)
    model.eval()

    args.output_dir.mkdir(parents=True, exist_ok=True)
    onnx_path = args.output_dir / args.onnx_name
    config_path = args.output_dir / args.config_name
    crf_path = args.output_dir / args.crf_name

    export_onnx(torch, model, raw_config["max_sequence_length"], onnx_path, args.opset)
    write_runtime_config(
        config_path,
        raw_config,
        bio_tags_to_keep,
        tags_to_keep,
        mapping_id_to_country,
        args.strict_before_inside,
    )
    write_crf_config(crf_path, model)

    print(f"wrote {onnx_path}")
    print(f"wrote {config_path}")
    print(f"wrote {crf_path}")
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root",
        type=Path,
        help="path to the upstream iso20022-address-structuring repo if it is not installed",
    )
    parser.add_argument("--weights", type=Path, required=True, help="upstream .safetensors checkpoint")
    parser.add_argument("--config", type=Path, required=True, help="upstream model .config.json")
    parser.add_argument("--output-dir", type=Path, default=Path("resources/models"))
    parser.add_argument("--onnx-name", default="address_transformer.onnx")
    parser.add_argument("--config-name", default="address_transformer.config.json")
    parser.add_argument("--crf-name", default="address_crf.json")
    parser.add_argument("--device", default="cpu")
    parser.add_argument("--opset", type=int, default=17)
    parser.add_argument(
        "--no-strict-before-inside",
        dest="strict_before_inside",
        action="store_false",
        help="allow I-* tags to start spans when the Go runtime groups BIO tags",
    )
    parser.set_defaults(strict_before_inside=True)
    return parser.parse_args()


def export_onnx(torch: Any, model: Any, max_sequence_length: int, output_path: Path, opset: int) -> None:
    class ONNXRuntimeWrapper(torch.nn.Module):
        """Thin module wrapper that exports emissions and country logits only."""

        def __init__(self, wrapped_model: Any):
            super().__init__()
            self.model = wrapped_model

        def forward(self, token_ids: Any, mask: Any) -> tuple[Any, Any]:
            bool_mask = mask.to(dtype=torch.bool)
            embeddings = self.model._produce_embeddings(token_ids, bool_mask)

            country_logits = torch.empty(
                (token_ids.shape[0], 0),
                dtype=embeddings.dtype,
                device=embeddings.device,
            )
            if self.model._has_country_classifier:
                cls_token, embeddings = embeddings[:, 0, :], embeddings[:, 1:, :]
                country_logits = self.model.country_predictor(cls_token)

            emissions = self.model._produce_emissions(embeddings)
            return emissions, country_logits

    wrapper = ONNXRuntimeWrapper(model)
    token_ids = torch.ones((1, max_sequence_length), dtype=torch.long, device=model.pos_embed.device)
    mask = torch.ones((1, max_sequence_length), dtype=torch.bool, device=model.pos_embed.device)

    torch.onnx.export(
        wrapper,
        (token_ids, mask),
        str(output_path),
        input_names=DEFAULT_INPUT_NAMES,
        output_names=DEFAULT_OUTPUT_NAMES,
        dynamic_axes={
            "token_ids": {0: "batch"},
            "mask": {0: "batch"},
            "emissions": {0: "batch"},
            "country_logits": {0: "batch"},
        },
        opset_version=opset,
    )


def write_runtime_config(
    path: Path,
    raw_config: dict[str, Any],
    bio_tags_to_keep: list[Any],
    tags_to_keep: list[Any],
    mapping_id_to_country: dict[int, str],
    strict_before_inside: bool,
) -> None:
    runtime_config = {
        "vocabulary": raw_config["vocabulary"],
        "bio_tags_to_keep": [model_dump_json(tag) for tag in bio_tags_to_keep],
        "tags_to_keep": [str(tag) for tag in tags_to_keep],
        "id_to_country": mapping_id_to_country,
        "strict_before_inside": strict_before_inside,
        "input_names": DEFAULT_INPUT_NAMES,
        "output_names": DEFAULT_OUTPUT_NAMES,
        "tag_count": len(bio_tags_to_keep),
        "max_sequence_length": raw_config["max_sequence_length"],
    }
    write_json(path, runtime_config)


def write_crf_config(path: Path, model: Any) -> None:
    crf = model.crf
    crf_config = {
        "start": tensor_to_list(crf.start_transitions),
        "end": tensor_to_list(crf.end_transitions),
        "transitions": tensor_to_list(crf.transitions),
        "transitions_order_2": tensor_to_list(crf.transitions_order_2),
    }
    write_json(path, crf_config)


def model_dump_json(value: Any) -> dict[str, Any]:
    if hasattr(value, "model_dump"):
        return value.model_dump(mode="json")
    return dict(value)


def tensor_to_list(value: Any) -> list[Any]:
    return value.detach().cpu().to(dtype=value.dtype).tolist()


def write_json(path: Path, value: Any) -> None:
    with path.open("w", encoding="utf-8") as file:
        json.dump(value, file, indent=2, sort_keys=True)
        file.write("\n")


if __name__ == "__main__":
    raise SystemExit(main())
