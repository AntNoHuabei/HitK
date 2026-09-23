from pathlib import Path
import os
import sys


ROOT = Path(__file__).resolve().parent
VENDOR = ROOT / "site-packages"
if VENDOR.is_dir():
    sys.path.insert(0, str(VENDOR))

from huggingface_hub import hf_hub_download
from docling_pp_ocrv6 import PPOCRv6Model
from docling.utils.model_downloader import download_models


CACHE = Path(os.environ.get("DOCLING_CACHE_DIR", str(ROOT / "models-cache")))
MODELS = CACHE / "models" / "PPOCRv6"
FILES = (
    ("PaddlePaddle/PP-OCRv6_medium_det_onnx", "61323801669c338b7891481ec7bac61ce31b576a", "inference.onnx", MODELS / "det"),
    ("PaddlePaddle/PP-OCRv6_medium_rec_onnx", "50c7eacafc52fa7bcf4194e8cd08e46f8558504b", "inference.onnx", MODELS / "rec"),
    ("PaddlePaddle/PP-OCRv6_medium_rec_onnx", "50c7eacafc52fa7bcf4194e8cd08e46f8558504b", "inference.yml", MODELS / "rec"),
)

for repo, revision, filename, target in FILES:
    target.mkdir(parents=True, exist_ok=True)
    hf_hub_download(repo_id=repo, revision=revision, filename=filename, local_dir=target)

PPOCRv6Model._ensure_rec_keys(MODELS / "rec")
# The standard PDF/image pipeline also needs layout and table structure
# checkpoints. Store them beside PP-OCRv6 and point Docling at this immutable
# directory at runtime, so first use cannot fall through to Hugging Face.
download_models(
    output_dir=CACHE / "models",
    progress=False,
    with_layout=True,
    with_tableformer=True,
    with_tableformer_v2=False,
    with_code_formula=False,
    with_picture_classifier=False,
    with_rapidocr=False,
)
print(MODELS)
