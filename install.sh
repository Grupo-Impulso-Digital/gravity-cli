#!/bin/sh
set -eu

REPO="Grupo-Impulso-Digital/gravity-cli"
BINARY="gravity"
: "${GRAVITY_VERSION:=1}"
: "${GRAVITY_INSTALL_DIR:=}"
: "${GRAVITY_RESOLVE_ONLY:=}"
: "${GRAVITY_RELEASES_API:=https://api.github.com}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1" >&2; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$1" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || err "curl is required"

api_get() {
  if [ -n "${GH_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: Bearer $GH_TOKEN" -H "Accept: application/vnd.github+json" "$1"
  else
    curl -fsSL -H "Accept: application/vnd.github+json" "$1"
  fi
}

platform() {
  os="$(uname -s)"
  case "$os" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *) err "unsupported OS '$os'; on Windows use install.ps1, Scoop or https://github.com/$REPO/releases" ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) err "unsupported architecture '$arch'" ;;
  esac
}

fields() {
  tr ',{}[]' '\n\n\n\n\n'
}

with_archive() {
  fields | grep '"browser_download_url"' | cut -d'"' -f4 | awk -v b="$BINARY" -v os="$os" -v arch="$arch" '
    {
      n = split($0, p, "/")
      if (n < 3 || p[n - 2] != "download") next
      tag = p[n - 1]
      if (tag !~ /^v/) next
      if (p[n] == b "_" substr(tag, 2) "_" os "_" arch ".tar.gz") print tag
    }'
}

newest_of_major() {
  major="$1"
  tags=""
  page=1
  while [ "$page" -le 5 ]; do
    json="$(api_get "$GRAVITY_RELEASES_API/repos/$REPO/releases?per_page=100&page=$page")" \
      || err "could not list the releases of $REPO"
    count="$(printf '%s\n' "$json" | fields | grep -c '"tag_name"' || true)"
    [ "$count" -gt 0 ] || break
    batch="$(printf '%s\n' "$json" | with_archive || true)"
    tags="$tags
$batch"
    [ "$count" -ge 100 ] || break
    page=$((page + 1))
  done
  printf '%s\n' "$tags" | awk -F. -v m="$major" '
    /^v[0-9]+\.[0-9]+\.[0-9]+$/ {
      maj = substr($1, 2)
      if (maj == m && (!found || $2 + 0 > b2 || ($2 + 0 == b2 && $3 + 0 > b3))) { found = 1; b2 = $2 + 0; b3 = $3 + 0 }
    }
    END { if (found) printf "v%s.%d.%d\n", m, b2, b3 }'
}

resolve_tag() {
  case "$GRAVITY_VERSION" in
    latest)
      json="$(api_get "$GRAVITY_RELEASES_API/repos/$REPO/releases/latest")" || err "could not resolve the latest release of $REPO"
      t="$(printf '%s\n' "$json" | fields | grep '"tag_name"' | head -n1 | cut -d'"' -f4)"
      [ -n "$t" ] || err "could not resolve the latest release of $REPO"
      if [ -z "$(printf '%s\n' "$json" | with_archive)" ]; then
        m="$(printf '%s\n' "$t" | sed -n 's/^v\([0-9][0-9]*\)\..*/\1/p')"
        [ -n "$m" ] || err "the latest release $t has no archive for $os/$arch"
        warn "the latest release $t has no archive for $os/$arch yet; using the newest v$m.x.y release that does"
        t="$(newest_of_major "$m")"
        [ -n "$t" ] || err "no v$m.x.y release of $REPO has an archive for $os/$arch"
      fi
      ;;
    [0-9]|[0-9][0-9]|v[0-9]|v[0-9][0-9])
      m="${GRAVITY_VERSION#v}"
      t="$(newest_of_major "$m")"
      [ -n "$t" ] || err "no v$m.x.y release of $REPO with an archive for $os/$arch exists yet"
      ;;
    v[0-9]*.[0-9]*.[0-9]*) t="$GRAVITY_VERSION" ;;
    [0-9]*.[0-9]*.[0-9]*) t="v$GRAVITY_VERSION" ;;
    *) err "GRAVITY_VERSION must be a major version (1), a release (v1.2.3) or latest; got '$GRAVITY_VERSION'" ;;
  esac
  printf '%s\n' "$t"
}

platform
tag="$(resolve_tag)"
if [ -n "$GRAVITY_RESOLVE_ONLY" ]; then
  printf '%s\n' "$tag"
  exit 0
fi

command -v tar >/dev/null 2>&1 || err "tar is required"

version="${tag#v}"
archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

info "Downloading $BINARY $tag ($os/$arch)"
curl -fsSL "$base/$archive" -o "$tmp/$archive" || err "download failed: $base/$archive"

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

if [ -n "$GRAVITY_INSTALL_DIR" ]; then
  dir="$GRAVITY_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
  dir="/usr/local/bin"
else
  dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || err "cannot create install dir $dir"

if ! install -m 0755 "$tmp/$BINARY" "$dir/$BINARY" 2>/dev/null; then
  if ! cp "$tmp/$BINARY" "$dir/$BINARY" 2>/dev/null; then
    err "could not write to $dir (set GRAVITY_INSTALL_DIR to a writable path, or re-run with sudo)"
  fi
  chmod 0755 "$dir/$BINARY"
fi

info "Installed $tag to $dir/$BINARY"
"$dir/$BINARY" version >&2 2>/dev/null || true

case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    warn "$dir is not on your PATH. Add it to your shell profile:"
    printf '  export PATH="%s:$PATH"\n' "$dir" >&2
    ;;
esac
