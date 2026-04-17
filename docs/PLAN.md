# Implementation plan — cmux-notify-bridge

Tracks GitHub issue #1. This is the source of truth for the Codex handoff;
AGENTS.md contains the permanent contract and CLAUDE.md the orchestration
rules.

## Deliverables (all on branch `feat/notify-bridge-1`)

| Path | Purpose |
| --- | --- |
| `go.mod` | `module github.com/zackey-heuristics/cmux-devcontainer-bridge`, `go 1.22` |
| `cmd/cmux-notify-bridge/main.go` | flag parsing, slog setup, notifier wiring, `http.Server` with graceful shutdown |
| `internal/server/server.go` | `http.Handler` for `/notify` + `/healthz`, token middleware, JSON decoding, size caps |
| `internal/server/server_test.go` | handler tests via `httptest` |
| `internal/notifier/notifier.go` | `Payload` struct, `Notifier` interface, shared error types |
| `internal/notifier/cmux.go` | `CmuxNotifier` with argv builder, binary resolution, timeout |
| `internal/notifier/cmux_test.go` | argv tests using a fake `execRunner` seam |
| `scripts/dc-exec.sh` | devcontainer-side helper (`devcontainer exec` with `CMUX_*` env forwarded) |
| `Makefile` | `build`, `test`, `vet`, `clean` targets |
| `README.md` | overwrites the stub — purpose, build, run, curl, hook example, launchd |

No other files. `.devcontainer/` stays untouched.

## Package shapes

### `internal/notifier`

```go
package notifier

import "context"

type Payload struct {
    Title       string
    Subtitle    string
    Body        string
    WorkspaceID string
    SurfaceID   string
    Source      string
    Kind        string
}

type Notifier interface {
    Notify(ctx context.Context, p Payload) error
}
```

`CmuxNotifier` fields:

```go
type CmuxNotifier struct {
    binPath string            // resolved once at construction
    dryRun  bool
    timeout time.Duration     // default 5s
    run     execRunner        // injectable for tests
    logger  *slog.Logger
}

type execRunner func(ctx context.Context, bin string, args []string) error
```

`buildArgs(p Payload) []string` is the pure helper under test. It always emits
`notify --title <T>` and conditionally appends the optional flags. Empty
optional fields produce no flag.

Binary resolution is `ResolveCmuxBin(explicit string) (string, error)`:
1. if `explicit != ""` → stat it, must be executable.
2. else if `os.Getenv("CMUX_BIN") != ""` → stat, must be executable.
3. else `exec.LookPath("cmux")`.

Errors wrapped with `fmt.Errorf("cmux binary not found: %w", err)` so the
handler can surface them verbatim.

### `internal/server`

```go
type Config struct {
    Token         string
    MaxBodyBytes  int64
    DefaultTitle  string
    Notifier      notifier.Notifier
    Logger        *slog.Logger
}

func NewHandler(cfg Config) http.Handler
```

Routing is explicit — a small `http.ServeMux` with three entries: `/notify`,
`/healthz`, catch-all 404. Method-specific dispatch inside each handler.

Token middleware checks `Authorization: Bearer <token>` with
`crypto/subtle.ConstantTimeCompare`. Applies to `/notify` only.

JSON decoding path:

1. Require `Content-Type` starts with `application/json` → 415 otherwise.
2. `http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)` → 413 on overflow.
3. `json.NewDecoder(body).Decode(&raw)` where `raw` is an inline struct with
   json tags. On decode error → 400. No `DisallowUnknownFields`.
4. Trim + cap each field (see AGENTS.md for the caps).
5. If title is empty after trim → substitute `cfg.DefaultTitle`.
6. `cfg.Notifier.Notify(ctx, payload)` with `ctx` from `r.Context()`. On
   error → 502; sanitize the error string (`err.Error()` with CR/LF stripped
   and length-capped at 256).
7. Success → 200 `{"ok":true}`.

Error body shape is always `{"ok":false,"error":"..."}`.

### `cmd/cmux-notify-bridge/main.go`

- Parse flags.
- Build slog handler (text, stderr, info or debug).
- Resolve cmux binary; on failure log fatal.
- Construct `CmuxNotifier`.
- Build handler.
- `http.Server` with `ReadHeaderTimeout: 5s`, `ReadTimeout: 10s`,
  `WriteTimeout: 10s`, `IdleTimeout: 60s`.
- Graceful shutdown on SIGINT/SIGTERM: 5s shutdown context.

## Test matrix

Server tests (table-driven):

| case | expect |
| --- | --- |
| `POST /notify` valid JSON, no token | 200 `{"ok":true}` |
| oversized body | 413 |
| malformed JSON | 400 |
| missing Content-Type | 415 |
| `GET /notify` | 405 |
| token set, no header | 401 |
| token set, wrong token | 401 |
| token set, correct token | 200 |
| notifier returns error | 502 and `error` field non-empty |
| `GET /healthz` | 200 even with token set |
| `POST /unknown` | 404 |

Notifier argv tests:

| payload | argv tail (after `notify`) |
| --- | --- |
| title only | `--title "Hi"` |
| title + body | `--title "Hi" --body "..."` |
| title + subtitle | `--title "Hi" --subtitle "..."` |
| title + workspace | `--title "Hi" --workspace ws1` |
| title + surface | `--title "Hi" --surface sf1` |
| all fields | title, subtitle, body, workspace, surface in that order |
| title with `--foo` prefix | argv still passes title as a single argv element; cmux receives `--foo` as a string, not a flag, because of argv separation |

Resolution tests: explicit path wins, env wins over PATH, both unset and PATH
lookup, binary missing returns typed error.

## Logging

- info on startup: `listen=... token=%t dry_run=%t cmux_bin=...` (bool for
  token presence, not the value).
- info on each request: method, path, status, duration, payload sizes.
- debug only: truncated title prefix (≤32 chars).
- Never log `Authorization` header, token value, or full body.

## Workflow in this repo

1. Claude Code has already written `CLAUDE.md`, `AGENTS.md`, `docs/PLAN.md` and
   created branch `feat/notify-bridge-1` off `main`.
2. Claude Code delegates `mode: implement` to Codex via `codex:rescue`.
3. Claude Code verifies: `go build ./...`, `go test ./...`, `go vet ./...` in
   this devcontainer.
4. Claude Code delegates `mode: adversarial-review` to Codex.
5. Claude Code applies fixes (or re-delegates for large changes).
6. Repeat until no critical/high, max 3 rounds.
7. Claude Code confirms with user, then commits, pushes, and opens a PR
   against `main` that `closes #1`.

## Known unknowns

- Whether cmux's workspace/surface refs are interchangeable with the UUID
  form supplied by the devcontainer's `CMUX_WORKSPACE_ID` env. Per the help
  output, both are accepted (`<id|ref>`), so we pass through as-is.
- Whether cmux accepts both `--workspace` and `--surface` simultaneously in
  all cases. Help lists them as independent flags. Pass both when both are
  set; if cmux rejects the combination at runtime, surface the error verbatim.
