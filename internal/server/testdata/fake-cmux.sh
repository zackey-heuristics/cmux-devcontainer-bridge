#!/usr/bin/env bash
# fake-cmux.sh — test fixture that records its argv to $FAKE_CMUX_CAPTURE.
# Each argument is written on its own line so tests can assert structure
# without worrying about whitespace inside individual arguments.
#
# Exit code controlled by $FAKE_CMUX_EXIT (default 0) so tests can also
# exercise the non-zero path.
set -eu

out="${FAKE_CMUX_CAPTURE:-/tmp/fake-cmux-capture.txt}"
: > "$out"
for arg in "$@"; do
  printf '%s\n' "$arg" >> "$out"
done

exit "${FAKE_CMUX_EXIT:-0}"
