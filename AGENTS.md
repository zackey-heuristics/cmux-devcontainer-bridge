---
# AGENTS.md — contract for Codex (and any delegated implementer)

Codex is invoked by Claude Code (see CLAUDE.md) as the implementer and as the
adversarial reviewer. This file is Codex's brief. Read it fully before touching
code.

## Project one-liner

A single Go binary `cmux-notify-bridge` that runs on the macOS host, listens on
`127.0.0.1:8765`, accepts `POST /notify` JSON from a devcontainer, and shells
out to the host-side `cmux notify` CLI.

## Invocation modes

Claude Code will call you in one of two modes. The mode is stated in the
prompt.

- **`mode: implement`** — Produce or modify code per `docs/PLAN.md`. Output a
  clear diff-shaped summary plus the list of files touched and the commands
  run (go build, go test). Do not invent requirements not in the plan; if you
  find a gap, stop and report it instead of guessing.
- **`mode: adversarial-review`** — Read the current working tree and produce a
  ranked list of concrete findings (critical / high / medium / low). Each
  finding must include: file:line, the bug or risk, a minimal reproduction or
  reasoning, and a suggested fix. Do not modify code in this mode. Be harsh
  but specific — "looks fragile" is not a finding; "map iteration in
  handler.go:42 makes argv order nondeterministic, breaking tests on Go 1.22+"
  is.

## Directory layout (authoritative)

```
cmd/cmux-notify-bridge/main.go        CLI flag parsing, server wiring, logging setup
internal/server/server.go             http.Handler, /notify, /healthz, token middleware
internal/server/server_test.go        handler + middleware tests (httptest)
internal/notifier/notifier.go         Notifier interface + Payload type
internal/notifier/cmux.go             CmuxNotifier (exec.Command) + argv builder
internal/notifier/cmux_test.go        argv-shape tests (via a fake exec runner)
scripts/dc-exec.sh                    devcontainer-side helper that forwards CMUX_* env
Makefile                              build / test / vet targets
README.md                             user-facing docs
go.mod                                module github.com/zackey-heuristics/cmux-devcontainer-bridge
```

Do not create additional packages. Do not add third-party dependencies. Do not
add a `pkg/` directory. Do not add CI config unless explicitly asked.

## Hard constraints

0. **Lint clean.** Before reporting done, run `make lint` and
   `make fmt-check`. Both must pass with zero issues. The linter stack is
   golangci-lint v2 (errcheck, govet, staticcheck, ineffassign, unused,
   revive, gocritic, misspell) + gofumpt + shellcheck. Config is in
   `.golangci.yml`.
1. **Stdlib only.** `net/http`, `encoding/json`, `os/exec`, `flag`, `log/slog`,
   `crypto/subtle`, `context`, `errors`, `io`, `strings`, `time`. Nothing else.
2. **No shell.** Use `exec.CommandContext(ctx, bin, args...)`. Never pass user
   input through `sh -c` or string-concatenated commands.
3. **Loopback default.** `--listen` defaults to `127.0.0.1:8765`.
4. **Body cap.** Wrap request bodies in `http.MaxBytesReader` using
   `--max-body-bytes` (default 16384). Decode with a standard `json.Decoder`;
   `DisallowUnknownFields` is NOT required (per spec, unknown fields are
   ignored).
5. **Field limits.** After decoding, trim and cap: `title` ≤ 256 chars,
   `subtitle` ≤ 256, `body` ≤ 4096, `workspace_id` ≤ 128, `surface_id` ≤ 128,
   `source` ≤ 64, `kind` ≤ 64. Truncate rather than reject; the hook should
   always succeed if at all possible.
6. **Missing title.** If `title` is empty after trim, substitute `--default-title`
   (itself defaulting to `"Claude Code"`).
7. **Token auth.** When `--token` is set, require
   `Authorization: Bearer <token>` on `/notify`. Compare with
   `crypto/subtle.ConstantTimeCompare`. `/healthz` is always open.
8. **cmux argv.**
   ```
   cmux notify --title <T> [--subtitle <S>] [--body <B>] [--workspace <W>] [--surface <SF>]
   ```
   Optional flags are omitted when the corresponding field is empty. The cmux
   binary is resolved in this order: `--cmux-bin` → `CMUX_BIN` env →
   `exec.LookPath("cmux")`. Missing binary → 502 with
   `{"ok":false,"error":"cmux binary not found"}`.
9. **Timeouts.** Wrap `cmux notify` execution in a `context.WithTimeout` of 5
   seconds. On timeout: 502 + `"cmux command timed out"`.
10. **--dry-run.** When set, the notifier logs the would-be argv and returns
    success without exec'ing. This is for local smoke tests.
11. **Logging.** Use `log/slog` with a text handler on stderr. `--verbose`
    switches to `slog.LevelDebug`. Never log token values. Log title length and
    body length, not their contents, at info level. Debug may include a
    truncated title prefix (≤32 chars).
12. **No fallback.** Do not fall back to `osascript` or anything else. cmux
    failures surface as 502.

## Notifier interface (required shape)

```go
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

`CmuxNotifier` implements `Notifier`. Expose an unexported seam for tests:

```go
type execRunner func(ctx context.Context, bin string, args []string) error
```

The concrete `CmuxNotifier` uses `exec.CommandContext` by default; tests inject
a fake runner and assert the exact argv.

## Handler contract

- `POST /notify`
  - `Content-Type: application/json` required (reject with 415 otherwise).
  - Body cap as above; oversized → 413.
  - Malformed JSON → 400 with `{"ok":false,"error":"invalid JSON"}`.
  - Notifier error → 502 with the sanitized error string.
  - Success → 200 with `{"ok":true}`.
- `GET /healthz` → 200 `{"ok":true}`.
- All other routes → 404.
- Methods other than documented → 405.

## Test expectations

- `internal/server/server_test.go`:
  - happy path 200
  - body over cap → 413
  - bad JSON → 400
  - missing Content-Type → 415
  - token configured + missing header → 401
  - token configured + wrong token → 401
  - token configured + correct token → 200
  - notifier error → 502 and error propagates into body
  - `/healthz` always 200 even with token configured
- `internal/notifier/cmux_test.go`:
  - argv when only title is set
  - argv when all fields are set
  - empty optional fields produce no corresponding flag
  - binary resolution: `--cmux-bin` wins over `CMUX_BIN` wins over PATH lookup
  - missing binary → typed error surfaced to handler as 502
  - context cancellation → typed timeout error

## Adversarial review focus areas

When running in `mode: adversarial-review`, pay extra attention to:

1. Command injection — any path from JSON to argv that is not length-limited or
   that could inject flag-like values (e.g., a `title` starting with `--`).
   The plan currently relies on positional safety of `exec.Command` + argv
   separation; confirm this holds for titles like `--title=pwn`.
2. Race conditions in server startup/shutdown and signal handling.
3. Goroutine leaks from the cmux subprocess if the request is cancelled.
4. Token comparison — reject anything that isn't constant-time.
5. Header/body parsing edge cases: empty body, trailing garbage, chunked
   encoding, gzip'd request (we do not advertise support — reject cleanly).
6. Log injection via CR/LF in title/body.
7. Environment leakage — the subprocess must inherit the parent env (cmux needs
   `CMUX_SOCKET_PATH`, `CMUX_SOCKET_PASSWORD`). Confirm `exec.Command` default
   behavior and document it.
8. Default values and their documentation drift vs README.

## Output format (implement mode)

Return:

1. **Files created or modified** — bulleted list with one-line purpose each.
2. **Key design choices** — up to 5 bullets. Only note deviations from this
   file or the plan.
3. **Commands run and their exit status** — at minimum `go build ./...`,
   `go test -race ./...`, `make lint`, `make fmt-check`, and
   `bash scripts/e2e.sh`.
4. **Open questions for Claude Code** — anything that blocked you or that you
   resolved with a guess.

## Output format (adversarial-review mode)

Return a markdown list grouped by severity:

```
### Critical
- file.go:LN — one-line title
  - Problem: ...
  - Why it matters: ...
  - Fix: ...

### High
...
### Medium
...
### Low
...
```

Do not pad with generic advice. If a severity bucket is empty, omit it.
