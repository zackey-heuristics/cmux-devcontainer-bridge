#!/usr/bin/env bash
# e2e.sh — shell-visible end-to-end smoke test.
#
# 1. Builds the bridge binary.
# 2. Points it at a fake cmux shim that captures its argv.
# 3. Starts the bridge on a fixed loopback port (127.0.0.1:18765, chosen to
#    avoid the default 127.0.0.1:8765 so a locally running bridge does not
#    collide with the smoke test).
# 4. POSTs /notify and /healthz via curl.
# 5. Verifies the HTTP response and the captured argv.
#
# This script is run by CI (.github/workflows/ci.yml) and by developers via
# `make smoke`.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

TMPDIR="$(mktemp -d)"
BRIDGE_PID=""

cleanup() {
  if [ -n "${BRIDGE_PID}" ] && kill -0 "${BRIDGE_PID}" 2>/dev/null; then
    kill "${BRIDGE_PID}" 2>/dev/null || true
    wait "${BRIDGE_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMPDIR}"
}
trap cleanup EXIT

# Build via make so we exercise the same target CI uses.
make build >/dev/null

FAKE_CMUX="${repo_root}/internal/server/testdata/fake-cmux.sh"
CAPTURE="${TMPDIR}/captured.txt"
BRIDGE_URL="http://127.0.0.1:18765"
BRIDGE_BIN="${repo_root}/bin/cmux-notify-bridge"

# Launch the bridge. Note: FAKE_CMUX_CAPTURE is inherited by the bridge and
# then by the cmux subprocess because CmuxNotifier uses os.Environ().
FAKE_CMUX_CAPTURE="${CAPTURE}" \
  "${BRIDGE_BIN}" \
    --listen "127.0.0.1:18765" \
    --cmux-bin "${FAKE_CMUX}" \
    >"${TMPDIR}/bridge.log" 2>&1 &
BRIDGE_PID=$!

# Wait for /healthz to respond.
ready=false
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl -sf "${BRIDGE_URL}/healthz" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.2
done
if [ "${ready}" != "true" ]; then
  echo "e2e: bridge did not become ready" >&2
  cat "${TMPDIR}/bridge.log" >&2 || true
  exit 1
fi

# POST a notification.
response="$(curl -sf -X POST "${BRIDGE_URL}/notify" \
  -H 'Content-Type: application/json' \
  -d '{"title":"smoke","body":"hello","workspace_id":"ws-smoke"}')"

if ! printf '%s' "${response}" | grep -q '"ok":true'; then
  echo "e2e: unexpected /notify response: ${response}" >&2
  cat "${TMPDIR}/bridge.log" >&2 || true
  exit 1
fi

# Give the fake cmux a moment to flush its capture file.
sleep 0.1

# The fake writes one arg per line. Assert key lines are present.
# Note: `grep -qx --` lets flag-like strings such as '--title' reach the pattern
# argument rather than being parsed as options.
if ! grep -qxF -- 'notify' "${CAPTURE}"; then
  echo "e2e: captured argv missing 'notify'" >&2
  cat "${CAPTURE}" >&2
  exit 1
fi
for expected in '--title' 'smoke' '--body' 'hello' '--workspace' 'ws-smoke'; do
  if ! grep -qxF -- "${expected}" "${CAPTURE}"; then
    echo "e2e: captured argv missing '${expected}'" >&2
    cat "${CAPTURE}" >&2
    exit 1
  fi
done

echo "e2e: OK"
