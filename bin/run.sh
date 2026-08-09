#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${HERDR_REVIEW_LOOP_BIN:-}" ]]; then
  exec "$HERDR_REVIEW_LOOP_BIN" "$@"
fi
root=$(cd "$(dirname "$0")/.." && pwd)
if [[ -x "$root/bin/herdr-review-loop" ]]; then
	exec "$root/bin/herdr-review-loop" "$@"
fi
if command -v herdr-review-loop >/dev/null 2>&1; then
  exec herdr-review-loop "$@"
fi
echo "herdr-review-loop binary not found; run make build or set HERDR_REVIEW_LOOP_BIN" >&2
exit 127
