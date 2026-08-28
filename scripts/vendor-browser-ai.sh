#!/usr/bin/env sh
set -eu

TRANSFORMERS_VERSION="3.7.1"
TRANSFORMERS_SHA512="cf5e325e49bb1bf14a18cf18db834b0dbad23ff91b9fad9bc8a358174e2967aa62753569b9ca9149d7ab420a5f416a0c56b84e2a6ad9b36f46df2435476f2c20"
ONNXRUNTIME_VERSION="1.22.0-dev.20250409-89f8206ba4"
ONNXRUNTIME_SHA512="d2e4bbe8e3e01f485608fac52a52fc918555edc90ceedff7e877db828170e8d7740995556d00b83e4ad1f26057f0bb4d50564edb921006a5761f1da1a371656d"
ONNXRUNTIME_COMMIT="89f8206ba4f1c22c39e0297fb55272e8ce8cd7d0"
ONNXRUNTIME_LICENSE_SHA256="2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c"
ONNXRUNTIME_NOTICES_SHA256="e9e90971a8e75a9a8ac0c6412e29c1202d079998389915aa485f46c816c3b4cc"

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname -- "$script_dir")
vendor_tmp=$(mktemp -d "${TMPDIR:-/tmp}/pictogrep-vendor.XXXXXX")
trap 'rm -r -- "$vendor_tmp"' EXIT HUP INT TERM

archive="$vendor_tmp/transformers.tgz"
curl --fail --location --proto '=https' --tlsv1.2 \
  "https://registry.npmjs.org/@huggingface/transformers/-/transformers-$TRANSFORMERS_VERSION.tgz" \
  -o "$archive"
printf '%s  %s\n' "$TRANSFORMERS_SHA512" "$archive" | sha512sum --check --status
tar -xzf "$archive" -C "$vendor_tmp"

# Rewrite one property access whose minified spelling matches the legacy
# HashiCorp Vault service-token shape (s. followed by 24 alphanumerics).
sed \
  -e 's#from"onnxruntime-common"#from"./ort.wasm.bundle.min.mjs"#' \
  -e 's#from"onnxruntime-web"#from"./ort.wasm.bundle.min.mjs"#' \
  -e 's#s[.]DebertaV2PreTrainedModel#s["DebertaV2PreTrainedModel"]#g' \
  "$vendor_tmp/package/dist/transformers.web.min.js" \
  > "$vendor_tmp/transformers.web.min.js"
if grep -q 'from"onnxruntime-' "$vendor_tmp/transformers.web.min.js"; then
  printf '%s\n' 'Unresolved ONNX Runtime import in Transformers.js.' >&2
  exit 1
fi
if grep -q 's[.]DebertaV2PreTrainedModel' "$vendor_tmp/transformers.web.min.js"; then
  printf '%s\n' 'Vault token false positive remains in Transformers.js.' >&2
  exit 1
fi
install -Dm644 "$vendor_tmp/transformers.web.min.js" \
  "$project_dir/web/transformers.web.min.js"

onnx_archive="$vendor_tmp/onnxruntime-web.tgz"
curl --fail --location --proto '=https' --tlsv1.2 \
  "https://registry.npmjs.org/onnxruntime-web/-/onnxruntime-web-$ONNXRUNTIME_VERSION.tgz" \
  -o "$onnx_archive"
printf '%s  %s\n' "$ONNXRUNTIME_SHA512" "$onnx_archive" | sha512sum --check --status
mkdir "$vendor_tmp/onnxruntime-web"
tar -xzf "$onnx_archive" -C "$vendor_tmp/onnxruntime-web"
install -Dm644 "$vendor_tmp/onnxruntime-web/package/dist/ort.wasm.bundle.min.mjs" \
  "$project_dir/web/ort.wasm.bundle.min.mjs"
sed 's/[[:space:]]*$//' \
  "$vendor_tmp/onnxruntime-web/package/dist/ort-wasm-simd-threaded.mjs" \
  > "$vendor_tmp/ort-wasm-simd-threaded.mjs"
install -Dm644 "$vendor_tmp/ort-wasm-simd-threaded.mjs" \
  "$project_dir/web/ort-wasm-simd-threaded.mjs"
install -Dm644 "$vendor_tmp/onnxruntime-web/package/dist/ort-wasm-simd-threaded.wasm" \
  "$project_dir/web/ort-wasm-simd-threaded.wasm"
install -Dm644 "$vendor_tmp/package/LICENSE" \
  "$project_dir/third_party/transformers.js/LICENSE"

onnx_base="https://raw.githubusercontent.com/microsoft/onnxruntime/$ONNXRUNTIME_COMMIT"
curl --fail --location --proto '=https' --tlsv1.2 "$onnx_base/LICENSE" \
  -o "$vendor_tmp/onnxruntime-LICENSE"
curl --fail --location --proto '=https' --tlsv1.2 "$onnx_base/ThirdPartyNotices.txt" \
  -o "$vendor_tmp/onnxruntime-ThirdPartyNotices.txt"
printf '%s  %s\n' "$ONNXRUNTIME_LICENSE_SHA256" "$vendor_tmp/onnxruntime-LICENSE" | sha256sum --check --status
printf '%s  %s\n' "$ONNXRUNTIME_NOTICES_SHA256" "$vendor_tmp/onnxruntime-ThirdPartyNotices.txt" | sha256sum --check --status
install -Dm644 "$vendor_tmp/onnxruntime-LICENSE" \
  "$project_dir/third_party/onnxruntime/LICENSE"
install -Dm644 "$vendor_tmp/onnxruntime-ThirdPartyNotices.txt" \
  "$project_dir/third_party/onnxruntime/ThirdPartyNotices.txt"

printf 'Vendored Transformers.js %s and ONNX Runtime %s.\n' \
  "$TRANSFORMERS_VERSION" "$ONNXRUNTIME_COMMIT"
