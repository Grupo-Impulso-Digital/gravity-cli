#!/bin/sh
# gravity-cli installer. The app's Download button hands users:
#
#   curl -fsSL https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.sh | sh
#
# It downloads the right prebuilt binary for the host OS/arch from the latest
# GitHub Release, verifies its checksum, and drops it on PATH. POSIX sh — no
# bashisms — so it runs under dash/sh as well as bash/zsh.
#
# Overrides (env vars):
#   GRAVITY_VERSION      tag to install (e.g. v0.1.0); default: latest
#   GRAVITY_INSTALL_DIR  install location; default: /usr/local/bin, else ~/.local/bin
set -eu

REPO="Grupo-Impulso-Digital/gravity-cli"
BINARY="gravity"
: "${GRAVITY_VERSION:=latest}"
: "${GRAVITY_INSTALL_DIR:=}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$1" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar  >/dev/null 2>&1 || err "tar is required"

# --- detect platform (match GoReleaser's default GOOS/GOARCH names) ----------
os="$(uname -s)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) err "unsupported OS '$os'. On Windows use Scoop or download from https://github.com/$REPO/releases" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "unsupported architecture '$arch'" ;;
esac

# --- resolve the release tag -------------------------------------------------
if [ "$GRAVITY_VERSION" = "latest" ]; then
  # /releases/latest excludes pre-releases, so this always resolves to stable.
  tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)"
  [ -n "$tag" ] || err "could not resolve the latest release (is the repo public and has it shipped a release yet?)"
else
  tag="$GRAVITY_VERSION"
fi
version="${tag#v}"

archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

# --- download ----------------------------------------------------------------
info "Downloading $BINARY $tag ($os/$arch)"
curl -fsSL "$base/$archive" -o "$tmp/$archive" \
  || err "download failed: $base/$archive"

# --- verify checksum (best-effort; hard-fail only on an actual mismatch) -----
if curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    sumcmd="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    sumcmd="shasum -a 256"
  else
    sumcmd=""
  fi
  if [ -n "$sumcmd" ]; then
    expected="$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')"
    actual="$(cd "$tmp" && $sumcmd "$archive" | awk '{print $1}')"
    [ -n "$expected" ] || err "no checksum listed for $archive"
    [ "$expected" = "$actual" ] || err "checksum mismatch for $archive (expected $expected, got $actual)"
    info "Checksum verified"
  else
    warn "no sha256 tool found; skipping checksum verification"
  fi
else
  warn "checksums.txt unavailable; skipping checksum verification"
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/$BINARY" ] || err "archive did not contain the $BINARY binary"

# --- choose install dir (no silent sudo) -------------------------------------
if [ -n "$GRAVITY_INSTALL_DIR" ]; then
  dir="$GRAVITY_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
  dir="/usr/local/bin"
else
  dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || err "cannot create install dir $dir"

if ! install -m 0755 "$tmp/$BINARY" "$dir/$BINARY" 2>/dev/null; then
  # `install` may be absent (busybox); fall back to cp + chmod.
  if ! cp "$tmp/$BINARY" "$dir/$BINARY" 2>/dev/null; then
    err "could not write to $dir (set GRAVITY_INSTALL_DIR to a writable path, or re-run with sudo)"
  fi
  chmod 0755 "$dir/$BINARY"
fi

info "Installed to $dir/$BINARY"
"$dir/$BINARY" version 2>/dev/null || true

# --- PATH hint ---------------------------------------------------------------
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    warn "$dir is not on your PATH. Add it to your shell profile:"
    printf '  export PATH="%s:$PATH"\n' "$dir" >&2
    ;;
esac
