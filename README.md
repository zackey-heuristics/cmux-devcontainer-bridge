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

## Quickstart

Drop-in setup for the common case: macOS host running `cmux`, a cmux
devcontainer, and Claude Code running inside it. Ready-made example files are
in [`examples/`](./examples).

1. **Install the bridge on the host.** Download the latest macOS binary from
   [Releases](https://github.com/zackey-heuristics/cmux-devcontainer-bridge/releases):

   ```bash
   # Pick your arch: darwin-arm64 (Apple Silicon) or darwin-amd64 (Intel)
   ARCH=darwin-arm64
   VERSION=v0.1.0   # replace with the latest tag from the Releases page

   curl -fsSL -o /tmp/cmux-notify-bridge.tar.gz \
     "https://github.com/zackey-heuristics/cmux-devcontainer-bridge/releases/download/${VERSION}/cmux-notify-bridge-${ARCH}.tar.gz"
   tar -xzf /tmp/cmux-notify-bridge.tar.gz -C /tmp
   sudo install /tmp/cmux-notify-bridge-${ARCH} /usr/local/bin/cmux-notify-bridge

   cmux-notify-bridge &
   curl -s http://127.0.0.1:8765/healthz   # → {"ok":true}
   ```

   For login-time autostart, see [Running at login with launchd](#running-at-login-with-launchd).
   To build from source instead, see [Build](#build).

2. **Install the Claude Code hooks.** Copy
   [`examples/.claude/settings.json`](./examples/.claude/settings.json) to
   `~/.claude/settings.json` (user-global) or to a project's
   `.claude/settings.json`. It registers `Notification`, `Stop`, and
   `SubagentStop` hooks that POST to `http://host.docker.internal:8765/notify`,
   tagging each payload with `$CMUX_WORKSPACE_ID` and `$CMUX_SURFACE_ID`.

3. **Drop the devcontainer overlay into the cmux devcontainer project.** Copy
   [`examples/.devcontainer/docker-compose.local.yml`](./examples/.devcontainer/docker-compose.local.yml)
   to `.devcontainer/docker-compose.local.yml` in the cmux devcontainer
   project. It:

   - bind-mounts `~/.claude` into the sandbox so the hooks from step 2 are
     visible inside the container,
   - adds `host.docker.internal:host-gateway` to the router and sandbox
     services so the sandbox can reach the bridge,
   - mounts `router-allow-bridge.sh` so the router opens only
     `127.0.0.1:8765/tcp`.

4. **Bring up the devcontainer.**

   ```bash
   devcontainer up --workspace-folder .
   ```

5. **Exec into the devcontainer with CMUX_\* forwarded.**

   ```bash
   ./scripts/dc-exec.sh bash
   ```

   This uses `devcontainer exec --remote-env` to forward `CMUX_WORKSPACE_ID`
   and `CMUX_SURFACE_ID` from the host so notifications carry the right
   workspace/surface tags.

6. **Use Claude Code inside the container.** Notification / Stop / SubagentStop
   events forward through the bridge and surface as cmux notifications on the
   host.

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

## Claude Code hook configuration

[`examples/.claude/settings.json`](./examples/.claude/settings.json) provides a
ready-made set of `Notification`, `Stop`, and `SubagentStop` hooks. Drop it
into `~/.claude/settings.json` (user-global) or a project's
`.claude/settings.json`. For each event the hook:

- builds the JSON payload with `jq` from the hook input (`hook_event_name`,
  `message`) plus `$CMUX_WORKSPACE_ID` / `$CMUX_SURFACE_ID`,
- POSTs it to `http://host.docker.internal:8765/notify`,
- runs `async: true` with `curl --max-time 3` and a trailing `|| true`, so the
  bridge being down never blocks or errors out a Claude Code session.

The hook depends on `jq` and `curl` being available inside the devcontainer.
If you run the bridge with `--token`, add an `Authorization: Bearer <token>`
header to the `curl` invocation.

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
# Install the binary first (see the Quickstart for the Releases tarball,
# or build from source):
# make build && sudo cp bin/cmux-notify-bridge /usr/local/bin/

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
