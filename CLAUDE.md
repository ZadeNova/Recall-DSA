# DSA Spaced Repetition Tool

## Overview

Personal spaced-repetition tool for LeetCode/NeetCode 150 practice. Tracks every problem solved and surfaces what's overdue for review, using SM-2. It does **not** decide what new problem to solve next — that's a manual decision made outside the tool.

**Full design spec: `docs/SPEC.md`.** This project has already been through extensive design iteration — read it before making any architectural decision. Most "obvious" alternatives (Postgres, a TUI, curriculum auto-guidance, FSRS, auth/multi-tenancy) were considered and deliberately rejected; see SPEC.md §13 before reintroducing any of them.

**Frontend design spec: `docs/FRONTEND.md`.** Colors, typography, page-by-page UX decisions, open UX questions, and frontend-specific implementation conventions live there, not here. Read it before making any visual or UX change, and record new frontend decisions there rather than only in chat.

**Post-v1 features: `docs/NEW_FEATURES.md`.** Designs, decisions and edge cases for features added after v1 (pause/archive, collections, etc.). Read it before working on any of them.

## Stack

- Go — backend, service layer, single binary
- SQLite — single-writer, personal-scale data
- htmx — server-rendered `html/template`, no build pipeline, no npm, no React/Vue
- Docker + docker-compose for packaging
- Self-hosted behind Tailscale — no application-layer auth, no multi-tenancy, one instance per person

## ⚠️ Critical: the SM-2 scheduler module

This is the one correctness-critical piece of the project. Full rules and a worked trace are in **SPEC.md §5**.

- Do not adjust the grading table, the first-interval values (1/3/5/7 days), the 45-day cap, or the Hard-specific ×1.2 rule without flagging it explicitly first — these were deliberately tuned through discussion, not placeholder defaults
- Write unit tests against the worked trace in SPEC.md §5 before treating this module as done. Bugs here are silent — they produce plausible-looking but wrong scheduling, not a crash
- Two specific bugs to check for, since they're the easiest way to get this subtly wrong:
  1. `repetitions` must reset to 0 only on Failed — Hard is still a pass and should increment it
  2. The 45-day cap applies _after_ computing the raw multiplicative interval (and after the Hard ×1.2 rule, if both could apply), not before

## Recommended build order

1. Schema (SPEC.md §3) — note `problems.slug` is the unique key, not `url`
2. Scheduler / SM-2 module + hand-checked unit tests — before anything else depends on its behavior
3. Service layer (SPEC.md §6)
4. HTTP handlers
5. htmx templates / UI, including the library view and add/edit/delete flows (SPEC.md §7, §9)
6. Bulk import (SPEC.md §9) — remember the baseline grade is Hard, not Good, and imports must stagger `next_review_date`, not default every row to the same date
7. Docker + Tailscale deployment (SPEC.md §10)

## Go Guidelines

- Write idiomatic, simple, modern Go. Prefer clarity and explicitness over cleverness or abstraction.
- Prefer the standard library; avoid unnecessary dependencies, frameworks, ORMs, and interfaces.
- Keep handlers thin; business logic belongs in the service layer and database access should remain separated.
- Handle errors explicitly, wrap errors with context using `%w`, and avoid unnecessary `panic`.
- Use `context.Context` for request-scoped operations and respect cancellation.
- Use goroutines/concurrency only when clearly justified; avoid leaks and uncontrolled shared state.
- Run `gofmt`, `go vet ./...`, and `go test ./...` before considering work complete.
- Before implementing non-trivial code, briefly explain the design and important trade-offs so the developer can understand and review the implementation.

## Git workflow

- Build every feature or non-trivial change on its own branch (e.g. `feature/pause-unpause`), never directly on `main`.
- Merge to `main` only once the work is complete and tested: `gofmt -l .`, `go vet ./...` and `go test ./...` are clean and the feature has been checked in the running app.
- Every push to `main` auto-deploys to the Pi (`.github/workflows/deploy.yml`), so `main` must always be deployable. A half-finished feature on `main` ships.
- Keep commits small and focused (one logical step each) with messages that explain why, not just what.
- Small docs-only changes may go straight to `main`.

## Explicitly out of scope

See SPEC.md §13 for the full list. If asked to add anything on it (Postgres, login, a TUI, live LeetCode API calls, curriculum-guidance/auto-recommend-next-problem), flag it rather than implementing it silently — these were removed on purpose, not overlooked.

## Commands

- Build: `go build ./...`
- Run: `go run ./cmd/server` (flags: `-db`, `-addr`, `-tz` — see `cmd/server/main.go`)
- Test: `go test ./...`
- Migrate: `internal/db/schema.sql` is idempotent DDL, applied on every startup. Adding a column to an existing table is the exception, since `CREATE TABLE IF NOT EXISTS` can't do it: `internal/db/db.go`'s `ensurePausedAtColumn` is the one guarded `ALTER TABLE` (nothing else in `schema.sql` may reference that column, because the schema runs before it). Seed data is gated separately (see `seedTopicsIfEmpty`).
- Deploy: see `docs/DEPLOY.md` (Docker + Tailscale, SPEC.md §10)
