#!/usr/bin/env sh
set -eu

REPOSITORY="tiagohierath/pictogrep"
INSTALL_VERSION="${PICTOGREP_VERSION:-latest}"
BIN_DIR="${PICTOGREP_BIN_DIR:-${XDG_BIN_HOME:-$HOME/.local/bin}}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
TARGET="$BIN_DIR/pictogrep"
HELPER_TARGET="$BIN_DIR/pictogrep-gallery-dl"

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'pictogrep installer: %s\n' "$*" >&2
  exit 1
}

case "${1:-}" in
  --help|-h)
    say "Usage: ./install.sh [--uninstall]"
    say "Installs one standalone binary into: $BIN_DIR"
    exit 0
    ;;
  --uninstall)
    if [ -x "$TARGET" ]; then
      "$TARGET" uninstall-desktop 2>/dev/null || true
    fi
    rm -f "$TARGET" "$TARGET.install-sh" "$HELPER_TARGET" \
      "$DATA_HOME/applications/pictogrep.desktop" \
      "$DATA_HOME/icons/hicolor/512x512/apps/pictogrep.png"
    say "Removed the Pictogrep application."
    say "Your pictures and boards remain in: $DATA_HOME/pictogrep"
    exit 0
    ;;
  "") ;;
  *) fail "unknown option: $1" ;;
esac

case "$(uname -s)" in
  Linux) ;;
  *) fail "this installer supports Linux; Windows builds are available on the Releases page" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) fail "unsupported CPU architecture: $(uname -m)" ;;
esac

SCRIPT_DIR=""
case "$0" in
  install.sh|*/install.sh)
    SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)
    ;;
esac
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/pictogrep-install.XXXXXX")
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

build_local() {
	[ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/go.mod" ] && [ -f "$SCRIPT_DIR/main.go" ] || return 1
	grep -Eq '^module[[:space:]]+github\.com/tiagohierath/pictogrep([[:space:]]*)$' "$SCRIPT_DIR/go.mod" || return 1
	command -v go >/dev/null 2>&1 || return 1
  say "Building the standalone Pictogrep binary…"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$TEMP_DIR/pictogrep" .)
}

download_release() {
  if [ "$INSTALL_VERSION" = "latest" ]; then
    URL="https://github.com/$REPOSITORY/releases/latest/download/pictogrep-linux-$ARCH.tar.gz"
  else
    URL="https://github.com/$REPOSITORY/releases/download/$INSTALL_VERSION/pictogrep-linux-$ARCH.tar.gz"
  fi
  say "Downloading Pictogrep for Linux/$ARCH…"
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --proto '=https' --tlsv1.2 "$URL" -o "$TEMP_DIR/pictogrep.tar.gz"
    curl --fail --location --proto '=https' --tlsv1.2 "$URL.sha256" -o "$TEMP_DIR/pictogrep.tar.gz.sha256" || true
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only -O "$TEMP_DIR/pictogrep.tar.gz" "$URL"
    wget --https-only -O "$TEMP_DIR/pictogrep.tar.gz.sha256" "$URL.sha256" || true
  else
    fail "install curl or wget, then run this installer again"
  fi
  if [ -s "$TEMP_DIR/pictogrep.tar.gz.sha256" ]; then
    expected=$(awk 'NR == 1 {print $1}' "$TEMP_DIR/pictogrep.tar.gz.sha256")
    case "$expected" in *[!0-9a-fA-F]*|'') fail "the release checksum is invalid" ;; esac
    [ "${#expected}" -eq 64 ] || fail "the release checksum is invalid"
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$TEMP_DIR/pictogrep.tar.gz" | awk '{print $1}')
    elif command -v openssl >/dev/null 2>&1; then
      actual=$(openssl dgst -sha256 "$TEMP_DIR/pictogrep.tar.gz" | awk '{print $NF}')
    else
      fail "sha256sum or openssl is required to verify Pictogrep"
    fi
    [ "$actual" = "$expected" ] || fail "the downloaded release failed checksum verification"
  else
    say "This older release has no checksum; continuing with HTTPS verification."
  fi
  command -v tar >/dev/null 2>&1 || fail "tar is required to unpack Pictogrep"
  tar -xzf "$TEMP_DIR/pictogrep.tar.gz" -C "$TEMP_DIR"
  [ -f "$TEMP_DIR/pictogrep" ] || fail "the downloaded release did not contain pictogrep"
}

if ! build_local; then
  download_release
fi

chmod 755 "$TEMP_DIR/pictogrep"
"$TEMP_DIR/pictogrep" version >/dev/null || fail "the downloaded binary could not run on this system"
if [ -f "$TEMP_DIR/gallery-dl" ]; then
  chmod 755 "$TEMP_DIR/gallery-dl"
  "$TEMP_DIR/gallery-dl" --version >/dev/null || fail "the included Pinterest downloader could not run on this system"
fi
mkdir -p "$BIN_DIR"
BIN_DIR=$(CDPATH= cd -- "$BIN_DIR" && pwd)
TARGET="$BIN_DIR/pictogrep"
HELPER_TARGET="$BIN_DIR/pictogrep-gallery-dl"
mv "$TEMP_DIR/pictogrep" "$TARGET.new"
mv "$TARGET.new" "$TARGET"
if [ -f "$TEMP_DIR/gallery-dl" ]; then
  mv "$TEMP_DIR/gallery-dl" "$HELPER_TARGET.new"
  mv "$HELPER_TARGET.new" "$HELPER_TARGET"
fi
printf '%s\n' "$TARGET" > "$TARGET.install-sh"

"$TARGET" install-desktop

say "Installed Pictogrep: $TARGET"
say "You can launch it from your applications menu."
case ":$PATH:" in
  *":$BIN_DIR:"*) say "Or run: pictogrep" ;;
  *)
    say "To also use it from a terminal, add this directory to PATH:"
    say "  $BIN_DIR"
    ;;
esac
