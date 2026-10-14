---
name: code-review
description: Review uncommitted Maze Royale changes and write a markdown review file
argument-hint: "[path or scope, e.g. internal/simulation]"
allowed-tools:
  - read
  - grep
  - glob
  - exec
permissions:
  allow:
    - Exec(git diff)
    - Exec(git status)
    - Exec(git log)
    - Exec(gofmt)
    - Exec(make test)
    - Exec(make vet)
    - Exec(node --test web/powerups.test.mjs)
---

Review the uncommitted changes in this repository and write the result to a markdown file.

## Scope

1. Run `git status --short` and `git diff HEAD` (include staged work; use `git diff` plus
   `git diff --staged` if `HEAD` is unavailable). If the user supplied a path or package,
   restrict the diff to it.
2. If there are no uncommitted changes, say so and stop — do not fall back to reviewing the
   branch or whole repo unless the user asks.
3. Read the full surrounding context of each changed file, not just the diff hunks. Trace
   callers and callees of changed functions before judging correctness.

## What to check

**Correctness and concurrency** — logic errors, off-by-one and bounds bugs in maze/grid code,
nil map and slice handling, error paths that swallow errors. The simulation is a tick loop with
shared player state: look for data races, mutexes held across network calls, goroutine leaks,
and unbuffered channel sends that can block a tick. Run the hot paths through `go test -race`
on the touched package when concurrency changed.

**Cross-service contracts** — gateway, matchmaking, simulation, maze and stats talk over HTTP
and websockets. Any change to a shared struct, JSON tag, or endpoint must be checked against
`internal/protocol/`, `docs/CONTRACT.md`, the peers that marshal or unmarshal it, and the
browser client in `web/`. Flag one-sided changes (server field renamed but `web/*.js` still
reads the old name, or vice versa).

**Service boundaries and config** — new peer URLs or ports must be overridable the way existing
ones are (`-port` flag plus the env vars listed in AGENTS.md), with defaults in
`internal/protocol/services.go` and inside the 8090-8094 range.

**Security** — the gateway assumes localhost-only traffic: `POST /internal/push` is
unauthenticated and websocket origin checks are disabled. Any new route, or any widening of
`cmd/publicproxy`'s method/path allowlist, is a finding unless it is clearly safe to expose.
Also check for logged secrets and unvalidated client input reaching the simulation.

**Go conventions** — match neighboring code per `.devin/rules/go-conventions.md`. Changed Go
files must be `gofmt`-clean. Dependencies are vendored: new imports need a `vendor/` entry, and
direct `go` commands need `-mod=vendor`.

**Tests** — does each behavior change have a test? Go changes should have package tests;
`web/` canvas/HUD changes belong in `web/powerups.test.mjs` (`node --test web/powerups.test.mjs`).
Note missing coverage as a finding.

## Verify, don't speculate

Run `make vet` and the tests for the touched packages (`make test` if the change is broad).
Report actual failures with their output. If you suspect a bug but cannot confirm it from the
code, label it "unverified" and say what evidence is missing. Do not invent findings to fill out
the report; "no issues in this category" is a valid result.

## Output

Write `.devin/reviews/<YYYY-MM-DD>-<short-slug>.md` (create the directory if needed; it is
gitignored) and print a short summary in chat with the file path and the blocking findings.

Use this structure:

```markdown
# Code review — <scope>

<date> · <N> files changed · commands run: <make vet, go test ./internal/... -race, ...>

## Verdict
<One paragraph: ship, ship with fixes, or needs rework, and why.>

## Blocking
- **<file>:<line> — <one-line title>**
  What is wrong, why it matters, and the suggested fix.

## Non-blocking
- **<file>:<line> — <title>** — <short note>

## Nits
- <file>:<line> — <short note>

## Verification
- `make vet` — pass
- `go test ./internal/simulation/... -race` — FAIL: <first failure>

## Not reviewed
<Anything out of scope or unverifiable, and what would be needed to check it.>
```

Order findings by severity, cite `file:line` for every one, and keep each finding to the
fewest words that make the problem and fix clear. Do not fix the code unless the user asks.
