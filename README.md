# cmux-notify-bridge

[![CI](https://github.com/zackey-heuristics/cmux-devcontainer-bridge/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/zackey-heuristics/cmux-devcontainer-bridge/actions/workflows/ci.yml)

## Purpose

`cmux-notify-bridge` is a small HTTP bridge that runs on your macOS host and
forwards Claude Code hook notifications from inside a devcontainer to the
host-side `cmux` CLI. Because devcontainers run in an isolated Linux
environment, they cannot directly execute host binaries like `cmux`. This
bridge solves the gap: the devcontainer sends an HTTP `POST /notify`, and the
bridge execs `cmux notify` on the host.

## Why

Claude Code supports [stop hooks](https://docs.anthropic.com/en/docs/claude-code/hooks)
that fire when a session ends. Inside a devcontainer the hook script cannot
call `cmux` directly. By running this bridge on the host and pointing the hook
at `host.docker.internal:8765`, you get native macOS notifications without
any extra dependencies inside the container.

## Build

```bash
# Using make (recommended):
make build
# Binary is written to ./bin/cmux-notify-bridge

# Or with go directly:
go build -o bin/cmux-notify-bridge ./cmd/cmux-notify-bridge
```

Requires Go 1.22 or later. No third-party dependencies.

## Run

```bash
./bin/cmux-notify-bridge [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--listen` | `127.0.0.1:8765` | TCP address to listen on |
| `--token` | _(empty)_ | Bearer token required on `POST /notify`; empty disables auth |
| `--cmux-bin` | _(auto)_ | Explicit path to the `cmux` binary; overrides `CMUX_BIN` and PATH |
| `--default-title` | `Claude Code` | Title used when the request body omits `title` |
| `--max-body-bytes` | `16384` | Maximum request body size in bytes |
| `--dry-run` | `false` | Log the would-be argv without executing `cmux`; useful for smoke tests |
| `--verbose` | `false` | Enable debug-level logging |

Binary resolution order for `cmux`:

1. `--cmux-bin` flag (if non-empty)
2. `CMUX_BIN` environment variable
3. `exec.LookPath("cmux")` (searches `$PATH`)

## API

### `POST /notify`

Send a notification to cmux.

**Request**

```
Content-Type: application/json
Authorization: Bearer <token>   # only required when --token is set
```

```json
{
  "title":        "Claude stopped",
  "subtitle":     "optional subtitle",
  "body":         "optional body text",
  "workspace_id": "optional cmux workspace id or ref",
  "surface_id":   "optional cmux surface id or ref",
  "source":       "optional source label",
  "kind":         "optional kind label"
}
```

Only `title` is meaningful for cmux; all other fields are optional. If `title`
is missing or blank, `--default-title` is used.

**Responses**

| Status | Body | Meaning |
|---|---|---|
| `200` | `{"ok":true}` | cmux executed successfully |
| `400` | `{"ok":false,"error":"invalid JSON"}` | Malformed request body |
| `401` | `{"ok":false,"error":"unauthorized"}` | Missing or wrong bearer token |
| `413` | `{"ok":false,"error":"request body too large"}` | Body exceeded `--max-body-bytes` |
| `415` | `{"ok":false,"error":"content-type must be application/json"}` | Wrong Content-Type |
| `502` | `{"ok":false,"error":"..."}` | cmux binary missing or execution failed |

**Examples**

```bash
# No auth:
curl -s -X POST http://127.0.0.1:8765/notify \
  -H "Content-Type: application/json" \
  -d '{"title":"Claude stopped"}'

# With bearer token:
curl -s -X POST http://127.0.0.1:8765/notify \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer mysecrettoken" \
  -d '{"title":"Claude stopped","body":"Session ended."}'
```

### `GET /healthz`

Health check. Always returns `200 {"ok":true}`. No auth required.

```bash
curl -s http://127.0.0.1:8765/healthz
# {"ok":true}
```

## Claude Code Stop-hook example

Add the following to your Claude Code settings as a stop hook. It fires when
a Claude Code session ends and sends a notification through the bridge.

**`.claude/settings.json`**

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "bash /path/to/notify-hook.sh"
          }
        ]
      }
    ]
  }
}
```

**`notify-hook.sh`** (bash + python variant, adapted from issue #1):

```bash
#!/usr/bin/env bash
# notify-hook.sh — Claude Code Stop hook that forwards to cmux-notify-bridge.
# Works both inside a devcontainer (via host.docker.internal) and on the host.

set -euo pipefail

BRIDGE_URL="${CMUX_BRIDGE_URL:-http://host.docker.internal:8765}"
TOKEN="${CMUX_BRIDGE_TOKEN:-}"

# Claude Code pipes the session JSON on stdin. Extract the last assistant
# message and trim it to 180 chars for the notification body.
PAYLOAD_JSON="$(cat)"
BODY="$(printf '%s' "$PAYLOAD_JSON" | python3 -c '
import json, re, sys
obj = json.load(sys.stdin)
msg = obj.get("last_assistant_message") or "Claude finished"
msg = re.sub(r"\s+", " ", msg).strip()
print(msg[:180] + ("..." if len(msg) > 180 else ""))
')"

PAYLOAD=$(python3 -c "
import json, os, sys
data = {
    'title': 'Claude Code',
    'body': os.environ.get('BODY', 'Session stopped.'),
    'workspace_id': os.environ.get('CMUX_WORKSPACE_ID', ''),
    'surface_id': os.environ.get('CMUX_SURFACE_ID', ''),
    'source': 'claude-code',
    'kind': 'stop',
}
print(json.dumps(data))
" BODY="$BODY")

# Build the curl argv as an array so that quoting and splitting are safe.
CURL_ARGS=(-s --max-time 5 -X POST "$BRIDGE_URL/notify" -H "Content-Type: application/json")
if [ -n "$TOKEN" ]; then
  CURL_ARGS+=(-H "Authorization: Bearer $TOKEN")
fi

curl "${CURL_ARGS[@]}" -d "$PAYLOAD" || true
```

Set `CMUX_BRIDGE_URL` if the bridge is on a non-default address.
Set `CMUX_BRIDGE_TOKEN` if `--token` is configured.

## scripts/dc-exec.sh

`scripts/dc-exec.sh` is a devcontainer-side helper that runs a command inside
the container while forwarding `CMUX_*` environment variables from the host.
This lets you test the notification flow end-to-end from the host shell.

```bash
# From the host, run curl inside the devcontainer, forwarding CMUX_* env:
./scripts/dc-exec.sh curl -s -X POST http://host.docker.internal:8765/notify \
  -H "Content-Type: application/json" \
  -d '{"title":"test from devcontainer"}'
```

## Running at login with launchd

Save the following plist as
`~/Library/LaunchAgents/com.zackey-heuristics.cmux-notify-bridge.plist`
and load it with `launchctl`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.zackey-heuristics.cmux-notify-bridge</string>

  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/cmux-notify-bridge</string>
    <string>--listen</string>
    <string>127.0.0.1:8765</string>
    <!-- Uncomment to require a bearer token:
    <string>--token</string>
    <string>mysecrettoken</string>
    -->
  </array>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>StandardErrorPath</key>
  <string>/tmp/cmux-notify-bridge.log</string>

  <key>EnvironmentVariables</key>
  <dict>
    <!-- cmux needs these to connect to its socket.
         CMUX_SOCKET_PATH defaults to
         ~/Library/Application Support/cmux/cmux.sock when unset, so you
         usually do not need to override it. Set CMUX_SOCKET_PASSWORD when
         you have configured a socket password in cmux's Settings. -->
    <!--
    <key>CMUX_SOCKET_PATH</key>
    <string>/Users/you/Library/Application Support/cmux/cmux.sock</string>
    <key>CMUX_SOCKET_PASSWORD</key>
    <string>your-cmux-socket-password</string>
    -->
  </dict>
</dict>
</plist>
```

```bash
# Install the binary first:
make build
sudo cp bin/cmux-notify-bridge /usr/local/bin/

# Load the agent:
launchctl load ~/Library/LaunchAgents/com.zackey-heuristics.cmux-notify-bridge.plist

# Check it is running:
launchctl list | grep cmux-notify-bridge

# Unload:
launchctl unload ~/Library/LaunchAgents/com.zackey-heuristics.cmux-notify-bridge.plist
```

## Development

```bash
make tools        # one-time: install golangci-lint v2 and gofumpt v0.7
make test         # go test -race -count=1 ./...
make lint         # golangci-lint run (errcheck, govet, staticcheck, revive, gocritic, ...)
make fmt          # gofumpt -w .
make fmt-check    # gofumpt -l .; fails if any file needs rewriting
make e2e          # Go integration test using testdata/fake-cmux.sh fixture
make smoke        # shell-visible e2e: builds, starts the bridge, POSTs via curl
make build        # produces ./bin/cmux-notify-bridge
```

Shell scripts are checked with `shellcheck --severity=warning` in CI.

### Release binaries

Push a `v*` tag (e.g. `git tag v0.1.0 && git push --tags`) and
`.github/workflows/release.yml` cross-builds `darwin/arm64` and
`darwin/amd64` binaries, packages them as `.tar.gz` with `.sha256` sidecars,
and attaches them to a GitHub Release.

## Security considerations

- **Loopback only by default.** `--listen` binds `127.0.0.1:8765`. Do not expose
  the port to other interfaces without also setting `--token`.
- **Bearer token is optional.** When `--token` is unset, anyone with loopback
  access (any user on the macOS host, any process in the devcontainer via
  `host.docker.internal`) can send notifications. Set `--token` if that is a
  concern.
- **Subprocess env inheritance.** The bridge launches `cmux` with the full
  parent environment (`os.Environ()`). This is required for cmux to pick up
  `CMUX_SOCKET_PATH` and `CMUX_SOCKET_PASSWORD`, but it also means every other
  host env var (credentials, API tokens, etc.) is visible to cmux. This is
  deliberate — the bridge trusts `cmux` the same way the logged-in user does —
  but worth knowing.
- **Input sanitisation.** The bridge trims, caps, and strips CR/LF from every
  payload field before passing it to argv or logs. Flag-like titles (e.g.,
  `"--body=pwned"`) are safe because `exec.Command` passes argv elements
  individually; `--title <value>` is two separate argv entries and the value
  is not re-parsed by a shell.
- **Logs.** Info-level logs record sizes and truncated title prefixes only.
  Bearer tokens and full bodies are never written to logs.

## License

See [LICENSE](LICENSE).
