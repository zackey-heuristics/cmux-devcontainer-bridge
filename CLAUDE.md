---
# CLAUDE.md — orchestration guide for Claude Code in this repository

This repository ships a small Go HTTP bridge (`cmux-notify-bridge`) that runs on the
macOS host and forwards Claude Code hook notifications from a devcontainer to the
host-side `cmux` CLI. Claude Code's role is to orchestrate the work; Codex is the
primary implementer and adversarial reviewer (see AGENTS.md).

## Scope

- The bridge is a single, small Go binary. Keep dependencies minimal — standard
  library only unless there is a strong reason.
- `.devcontainer/` is a pre-existing shared config owned by the host devcontainer
  project. **Do not modify it.** Any devcontainer-side glue (e.g., `dc-exec.sh`)
  lives at the repo root or under `scripts/`.
- The cmux CLI runs on the macOS host only. It is not available inside this
  devcontainer. The bridge therefore cannot be end-to-end tested from here — it
  must build and unit-test cleanly on Linux, and be runnable on macOS.

## Claude Code's responsibilities

1. **User-facing communication.** Claude Code is the only agent that talks to the
   user. Codex runs as a subagent and its output is internal.
2. **Planning.** Claude Code authors and maintains `docs/PLAN.md`. The plan is
   the source of truth for Codex handoffs. When requirements change, update the
   plan first, then re-brief Codex.
3. **Spec clarification.** When anything is ambiguous (cmux argv, flag shape,
   fallback policy, layout), ask the user using `AskUserQuestion`. Do not guess
   when the guess would be load-bearing.
4. **Codex handoff.** Delegate implementation and adversarial review to Codex via
   the `codex:rescue` subagent (see AGENTS.md for the handoff contract). Claude
   Code writes the prompts and picks what to accept.
5. **Review integration.** Codex returns a patch or review findings. Claude Code
   inspects the diff, applies fixes directly when small, or re-delegates to
   Codex when larger. Never report "done" based on Codex's self-summary — read
   the actual files.
6. **Git hygiene.** Claude Code creates branches, commits, and PRs, but always
   confirms with the user before `git push`, `gh pr create`, `gh issue create`,
   or any `git` operation that rewrites or deletes history.
7. **Iteration loop.** Run the implement → adversarial-review → fix cycle until
   Codex's adversarial review produces no critical/high findings, up to 3
   rounds. Summarize each round for the user.

## Explicit non-goals

- Do not add a notification fallback (e.g., `osascript`). If the cmux CLI is
  missing or fails, respond with `502 {"ok":false,"error":"..."}` and log.
- Do not bake in cmux socket-auth (`--password`, `CMUX_SOCKET_PASSWORD`) beyond
  passing the environment through. The bridge inherits the host user's
  environment; it is not a cmux auth layer.
- Do not introduce a config file, TLS, mutual auth, or a web UI. This is a
  loopback-only helper.
- Do not touch `.devcontainer/`.

## Guardrails Claude Code must enforce on Codex output

- `exec.Command(bin, args...)` only — never `sh -c`, never string concatenation
  into a shell.
- Trim + length-cap every user-supplied string before it hits argv or logs.
- JSON decoder with an `io.LimitReader`-wrapped body (default cap 16 KiB,
  overridable via `--max-body-bytes`).
- Bearer token comparison uses `crypto/subtle.ConstantTimeCompare`.
- Loopback default (`127.0.0.1:8765`). If the user asks to listen on `0.0.0.0`,
  Claude Code should surface that as a risk in the PR description.
- Logs must not include token values or full bodies. Log sizes and truncated
  titles only.

## Build / test commands (reference)

```
make tools            # one-time: install golangci-lint and gofumpt
make build            # builds cmd/cmux-notify-bridge to ./bin/
make test             # go test -race -count=1 ./...
make vet              # go vet ./...
make lint             # golangci-lint run
make fmt              # gofumpt -w .
make fmt-check        # gofumpt -l .; fail if non-empty
make e2e              # Go integration test (testdata/fake-cmux.sh fixture)
make smoke            # build + scripts/e2e.sh (shell smoke)
```

CI (`.github/workflows/ci.yml`) runs the same checks on every push/PR. The
release workflow (`.github/workflows/release.yml`) builds darwin/arm64 +
darwin/amd64 binaries on `v*` tags and attaches them to a GitHub Release.

## Definition of done

- `make test`, `make lint`, `make fmt-check`, `make smoke` all pass on Linux
  arm64 (this devcontainer).
- CI (GitHub Actions) passes the same checks plus `shellcheck` on the host
  runner.
- `cmd/cmux-notify-bridge` produces a single static binary usable on macOS.
- Handler unit tests cover: happy path, oversized body, bad JSON, missing title,
  token required/accepted/rejected, notifier failure surfaces as 502.
- Notifier tests confirm argv shape for the full matrix of optional fields.
- E2E tests (Go and shell) exercise the full request path against a fake cmux
  fixture.
- README documents: purpose, build, run flags, curl examples, Claude Code hook
  example, `dc-exec.sh` usage, launchd plist snippet, and a Development section
  with the make targets.
- Any unresolved questions (e.g., whether `cmux notify` accepts both workspace
  and surface together in practice) are called out at the bottom of the PR.
