# Third-party browser runtime

Pictogrep embeds the browser runtime instead of executing JavaScript from a
CDN. Run `scripts/vendor-browser-ai.sh` to reproduce these files from pinned,
checksum-verified npm archives. The script points Transformers.js package
imports at the local ONNX Runtime bundle.

- `@huggingface/transformers` 3.7.1 — Apache-2.0
- `onnxruntime-web` 1.22.0-dev.20250409-89f8206ba4 — MIT

The CLIP model is fetched separately on first use from the immutable Hugging
Face revision recorded in `embedding_model.go` and cached by the browser.
