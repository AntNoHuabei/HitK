from __future__ import annotations

import argparse
import json
import mimetypes
import os
from pathlib import Path
import sys
from typing import Any


SKILL_ROOT = Path(__file__).resolve().parent
PAYLOAD_VALUE = os.environ.get("HITK_SKILL_PAYLOAD", "").strip()
PAYLOAD_ROOT = Path(PAYLOAD_VALUE).resolve() if PAYLOAD_VALUE else SKILL_ROOT
VENDOR = PAYLOAD_ROOT / "site-packages"
if VENDOR.is_dir():
    sys.path.insert(0, str(VENDOR))


def parse_request() -> tuple[str, argparse.Namespace, Path]:
    request = json.load(sys.stdin)
    command = str(request.get("command", "")).strip().lower()
    parser = argparse.ArgumentParser(add_help=False, allow_abbrev=False)
    parser.add_argument("--path", required=True)
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--limit", type=int, default=96 * 1024)
    args = parser.parse_args(request.get("args") or [])
    path = Path(args.path).resolve(strict=True)
    if not path.is_file():
        raise ValueError("document path is not a file")
    workspace = Path(str(request.get("workspace", ""))).resolve(strict=True)
    return command, args, workspace


def converter():
    from docling.datamodel.base_models import InputFormat
    from docling.datamodel.pipeline_options import PdfPipelineOptions
    from docling.document_converter import DocumentConverter, ImageFormatOption, PdfFormatOption
    from docling_pp_ocrv6 import PPOCRv6Options

    pipeline = PdfPipelineOptions(do_ocr=True)
    pipeline.ocr_options = PPOCRv6Options(lang=["zh", "en"])
    pipeline.allow_external_plugins = True
    pipeline.artifacts_path = PAYLOAD_ROOT / "models-cache" / "models"
    return DocumentConverter(
        format_options={
            InputFormat.PDF: PdfFormatOption(pipeline_options=pipeline),
            InputFormat.IMAGE: ImageFormatOption(pipeline_options=pipeline),
        }
    )


def convert_document(path: Path) -> tuple[str, dict[str, Any]]:
    result = converter().convert(path)
    markdown = result.document.export_to_markdown()
    payload = result.document.export_to_dict()
    return markdown, payload


def safe_stem(path: Path) -> str:
    value = "".join(ch if ch.isalnum() or ch in "-_" else "_" for ch in path.stem)
    return value or "document"


def artifact_dir(workspace: Path) -> Path:
    import secrets

    target = workspace / ".hitk" / "artifacts" / f"document_{secrets.token_hex(6)}"
    target.mkdir(parents=True, exist_ok=False)
    return target


def main() -> None:
    command, args, workspace = parse_request()
    source = Path(args.path).resolve()
    mime = mimetypes.guess_type(source.name)[0] or "application/octet-stream"
    if command == "inspect":
        stat = source.stat()
        content = f"文件：{source.name}\n格式：{source.suffix.lower().lstrip('.')}\n大小：{stat.st_size} 字节"
        print(json.dumps({"content": content, "display": f"检查 {source.name}"}, ensure_ascii=False))
        return
    if command not in {"extract", "open", "convert"}:
        raise ValueError(f"unsupported document command: {command}")

    markdown, structured = convert_document(source)
    offset = max(args.offset, 0)
    limit = min(max(args.limit, 1024), 256 * 1024)
    content = markdown[offset : offset + limit]
    truncated = offset + limit < len(markdown)
    artifacts: list[dict[str, Any]] = []
    if command == "convert":
        target = artifact_dir(workspace)
        stem = safe_stem(source)
        markdown_path = target / f"{stem}.md"
        json_path = target / f"{stem}.docling.json"
        markdown_path.write_text(markdown, encoding="utf-8")
        json_path.write_text(json.dumps(structured, ensure_ascii=False, indent=2), encoding="utf-8")
        artifacts = [
            {"path": str(markdown_path), "name": markdown_path.name, "mime": "text/markdown", "operation": "created"},
            {"path": str(json_path), "name": json_path.name, "mime": "application/json", "operation": "created"},
        ]
    print(
        json.dumps(
            {
                "content": content,
                "display": f"{command} {source.name}",
                "artifacts": artifacts,
                "metadata": {"mime": mime, "format": source.suffix.lower().lstrip("."), "offset": offset},
                "truncated": truncated,
            },
            ensure_ascii=False,
        )
    )


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"document skill failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
