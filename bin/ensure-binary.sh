#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
binary="$root/bin/herdr-review-loop"
version=$(awk -F '"' '$1 ~ /^version[[:space:]]*=/ { print $2; exit }' "$root/herdr-plugin.toml")

accepts_version() {
  [[ -x "$binary" ]] && [[ "$("$binary" version 2>/dev/null | tr -d '[:space:]')" == "$version" ]]
}

build() {
  command -v go >/dev/null 2>&1 || return 1
  go build -o "$binary" -ldflags "-X main.version=$version" ./cmd/herdr-review-loop
  accepts_version
}

download() {
  local_os=$(uname -s | tr '[:upper:]' '[:lower:]')
  local_arch=$(uname -m)
  case "$local_os" in linux|darwin) ;; *) return 1 ;; esac
  case "$local_arch" in x86_64|amd64) local_arch=amd64 ;; arm64|aarch64) local_arch=arm64 ;; *) return 1 ;; esac
  asset="herdr-review-loop-$local_os-$local_arch"
  temporary=$(mktemp -d)
  trap 'rm -rf "$temporary"' RETURN
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release download "v$version" --repo mikhail-angelov/herdr-review-loop --pattern "$asset" --pattern SHA256SUMS --dir "$temporary" >/dev/null 2>&1 || true
  fi
  if [[ ! -f "$temporary/$asset" || ! -f "$temporary/SHA256SUMS" ]] && command -v curl >/dev/null 2>&1; then
    rm -f "$temporary/$asset" "$temporary/SHA256SUMS"
    base="https://github.com/mikhail-angelov/herdr-review-loop/releases/download/v$version"
    curl -fsSL "$base/$asset" -o "$temporary/$asset" && curl -fsSL "$base/SHA256SUMS" -o "$temporary/SHA256SUMS" || true
  fi
  [[ -f "$temporary/$asset" && -f "$temporary/SHA256SUMS" ]] || return 1
  if command -v sha256sum >/dev/null 2>&1; then verifier=(sha256sum -c -)
  elif command -v shasum >/dev/null 2>&1; then verifier=(shasum -a 256 -c -)
  else return 1
  fi
  checksum=$(awk -v asset="$asset" '$2 == asset { print; exit }' "$temporary/SHA256SUMS")
  [[ -n "$checksum" ]] || return 1
  (cd "$temporary" && printf '%s\n' "$checksum" | "${verifier[@]}") >/dev/null 2>&1 || return 1
  chmod +x "$temporary/$asset"
  mv "$temporary/$asset" "$binary"
  accepts_version || { rm -f "$binary"; return 1; }
}

case "${1:-}" in
  --build)
    build || { echo "cannot build herdr-review-loop; install Go 1.26 or use a released checkout" >&2; exit 1; }
    ;;
  --in-tree)
    accepts_version || download || build || { echo "cannot provision herdr-review-loop; install Go 1.26 (then make build), or use a released checkout with gh or curl" >&2; exit 1; }
    ;;
  "")
    if [[ -n "${HERDR_REVIEW_LOOP_BIN:-}" || -x "$binary" ]] || command -v herdr-review-loop >/dev/null 2>&1; then exit 0; fi
    echo "herdr-review-loop binary not found; run make build or set HERDR_REVIEW_LOOP_BIN" >&2
    exit 127
    ;;
  *) echo "usage: $0 [--in-tree|--build]" >&2; exit 2 ;;
esac
