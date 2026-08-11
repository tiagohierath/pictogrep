#!/usr/bin/env sh
set -eu

REPOSITORY="tiagohierath/pictogrep"
INSTALL_VERSION="${PICTOGREP_VERSION:-latest}"
BIN_DIR="${PICTOGREP_BIN_DIR:-${XDG_BIN_HOME:-$HOME/.local/bin}}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
APPLICATIONS_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
TARGET="$BIN_DIR/pictogrep"

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
    rm -f "$TARGET" "$APPLICATIONS_DIR/pictogrep.desktop"
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

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/pictogrep-install.XXXXXX")
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

build_local() {
  command -v go >/dev/null 2>&1 || return 1
  [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/go.mod" ] || return 1
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
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only -O "$TEMP_DIR/pictogrep.tar.gz" "$URL"
  else
    fail "install curl or wget, then run this installer again"
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
mkdir -p "$BIN_DIR"
mv "$TEMP_DIR/pictogrep" "$TARGET.new"
mv "$TARGET.new" "$TARGET"

mkdir -p "$APPLICATIONS_DIR"
cat > "$APPLICATIONS_DIR/pictogrep.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Pictogrep
Comment=Find and storyboard your pictures
Exec=$TARGET
Terminal=false
Categories=Graphics;Photography;
Keywords=images;search;storyboard;
EOF

say "Installed Pictogrep: $TARGET"
say "You can launch it from your applications menu."
case ":$PATH:" in
  *":$BIN_DIR:"*) say "Or run: pictogrep" ;;
  *)
    say "To also use it from a terminal, add this directory to PATH:"
    say "  $BIN_DIR"
    ;;
esac
