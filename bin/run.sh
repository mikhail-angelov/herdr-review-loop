#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${HERDR_REVIEW_LOOP_BIN:-}" ]]; then
  exec "$HERDR_REVIEW_LOOP_BIN" "$@"
fi
root=$(cd "$(dirname "$0")/.." && pwd)
binary="$root/bin/herdr-review-loop"

# an upgrade replaces the manifest and leaves the binary beside it untouched, and every action and
# pane comes through here — so this is where a binary from the version before is provisioned rather
# than run. A binary that cannot be replaced is still run: a plugin that does the old thing beats
# one that does nothing, and the warning says which happened.
if [[ -f "$root/herdr-plugin.toml" ]]; then
  version=$(awk -F '"' '$1 ~ /^version[[:space:]]*=/ { print $2; exit }' "$root/herdr-plugin.toml")
  if [[ ! -x "$binary" || "$("$binary" version 2>/dev/null | tr -d '[:space:]')" != "$version" ]]; then
    bash "$root/bin/ensure-binary.sh" --in-tree ||
      echo "herdr-review-loop: could not provision $version; running the binary that is here" >&2
  fi
fi

if [[ -x "$binary" ]]; then
	exec "$binary" "$@"
fi
if command -v herdr-review-loop >/dev/null 2>&1; then
  exec herdr-review-loop "$@"
fi
echo "herdr-review-loop binary not found; run make build or set HERDR_REVIEW_LOOP_BIN" >&2
exit 127
