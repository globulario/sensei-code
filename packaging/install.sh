#!/bin/sh
# Sensei Code installer.
#
#   curl -fsSL https://raw.githubusercontent.com/globulario/sensei-code/main/packaging/install.sh | sh
#
# Installs Sensei Code and, when it is missing, Sensei itself: the tool is
# useless without the graph it reads, and telling someone to go and install a
# second thing first is where adoption stops. Prefers the platform package
# manager so upgrades stay in one place, and falls back to a release tarball.
#
# It never edits a shell profile. A script that silently rewrites PATH is harder
# to undo than one that prints the line to add.
set -eu

REPO="globulario/sensei-code"
TAP="globulario/tap"
BIN_DIR="${SENSEI_CODE_BIN_DIR:-$HOME/.local/bin}"

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { warn "install failed: $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

platform() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture: $arch" ;;
  esac
  case "$os" in
    linux|darwin) ;;
    *) die "unsupported system: $os (use winget on Windows)" ;;
  esac
  printf '%s-%s' "$os" "$arch"
}

install_with_brew() {
  say "==> installing with Homebrew"
  brew tap "$TAP" >/dev/null 2>&1 || true
  # Sensei first: sensei-code checks for it at startup and can repair much less
  # without it.
  have sensei || brew install "$TAP/sensei"
  brew install "$TAP/sensei-code"
}

install_from_release() {
  target=$(platform)
  say "==> downloading sensei-code for $target"
  mkdir -p "$BIN_DIR"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  url="https://github.com/$REPO/releases/latest/download/sensei-code-$target.tar.gz"
  if have curl; then
    curl -fsSL "$url" -o "$tmp/sensei-code.tar.gz" || die "could not download $url"
  elif have wget; then
    wget -qO "$tmp/sensei-code.tar.gz" "$url" || die "could not download $url"
  else
    die "neither curl nor wget is available"
  fi
  tar -xzf "$tmp/sensei-code.tar.gz" -C "$tmp" || die "the downloaded archive could not be read"
  found=$(find "$tmp" -name sensei-code -type f | head -1)
  [ -n "$found" ] || die "the archive contained no sensei-code binary"
  install -m 0755 "$found" "$BIN_DIR/sensei-code"
  say "    installed $BIN_DIR/sensei-code"

  if ! have sensei; then
    warn ""
    warn "Sensei itself is not installed, and sensei-code reads its graph."
    warn "Install it with:  brew install $TAP/sensei"
    warn "or from:          https://github.com/globulario/sensei/releases"
  fi
}

main() {
  if have brew; then
    install_with_brew
  else
    install_from_release
  fi

  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
      say ""
      say "$BIN_DIR is not on your PATH. Add this to your shell profile:"
      say "    export PATH=\"$BIN_DIR:\$PATH\""
      ;;
  esac

  say ""
  say "Installed. In a repository, run:"
  say "    sensei-code setup --apply    # check everything and repair what it can"
  say "    sensei-code                  # start"
}

main "$@"
