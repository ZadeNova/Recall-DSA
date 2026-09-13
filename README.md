# Recall-DSA

A personal spaced-repetition tool for leetcode hmm.
(SM-2 scheduling) — but honestly, the app itself is secondary. This was
mainly a hobby project to experiment with:

- AI-assisted development workflow (Experimenting with claude code for fun)
- Go as a backend language
- CI/CD: GitHub Actions → build → GHCR → deploy
- Self-hosting on a Raspberry Pi behind Tailscale (homelab-style)

## Stack

- Go — backend, service layer, single binary
- SQLite — single-writer, personal-scale data
- htmx — server-rendered `html/template`, no build pipeline, no npm
- Docker + docker-compose for packaging

## Run locally

```
go run ./cmd/server
```

Flags (all optional): `-db` (default `recall.db`), `-addr` (default
`127.0.0.1:8080`), `-tz` (default `Asia/Singapore` — set to your own if
self-hosting).

## CI/CD & deployment

Push to `main` → GitHub Actions runs `go test`, then cross-compiles an
arm64 Docker image (native Go cross-compilation, no QEMU) and pushes it
to GHCR, then SSHes into a Raspberry Pi over Tailscale to pull and
restart the container. No manual deploy step for an ordinary change.

## Docs

- [SPEC.md](SPEC.md) — full design spec; read before any architectural
  change
- [FRONTEND.md](FRONTEND.md) — UX/visual decisions
- [DEPLOY.md](DEPLOY.md) — Pi setup and the CI/CD pipeline in detail
- [CLAUDE.md](CLAUDE.md) — dev commands, build order, guidelines for
  AI-assisted work on this repo
