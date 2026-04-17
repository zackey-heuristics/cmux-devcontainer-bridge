#!/usr/bin/env bash
# dc-exec.sh — run a command inside the devcontainer while forwarding the
# cmux workspace/surface identifiers from the host. The cmux-notify-bridge
# itself does not use this script; it is a helper for launching devcontainer
# processes (e.g. `bash`, or Claude Code) with CMUX_* context plumbed through.
#
# Usage:
#   ./dc-exec.sh <command> [args...]
# Examples:
#   ./dc-exec.sh bash
#   ./dc-exec.sh env | grep CMUX
set -eu

# @devcontainers/cli `exec` documents `--remote-env KEY=VALUE` for forwarding
# env into the container (there is no `-e` short form in the published CLI).
devcontainer exec \
  --workspace-folder . \
  --remote-env CMUX_WORKSPACE_ID="${CMUX_WORKSPACE_ID:-}" \
  --remote-env CMUX_SURFACE_ID="${CMUX_SURFACE_ID:-}" \
  -- \
  "$@"
