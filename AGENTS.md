# AGENTS.md — working on ask

Conventions for anyone (agent or human) working **on the `ask` codebase itself** — how to
develop, test, and reason about changes here. This is build-side; it is *not* how to *use*
`ask` as an agent-to-human inbox (that lives in the `ask` skill — `ask install-skill`, then
`ask help`). Written for a contributor with no prior context.

`ask` is a single Go binary providing CLI + MCP for an agent-to-human request inbox:
agents file requests for actions only a human can take, the human resolves them, a verifier
confirms, the loop closes. Per-item JSON lives under `.ask/`. The design is converged;
implementation is plan-driven.

## Where things live (doc boundaries)

Content drifts into the wrong surface without explicit boundaries. Place content by these
rules, and move it when you find it misfiled:

- **The `ask` skill (`skills/ask/SKILL.md`)** — how to *use* ask: the file → resolve →
  verify → close loop, when to file vs. ask in chat, urgency/verifier discipline. Not
  anything *above* ask (worker dispatch, orchestration checkpoints) — that belongs to
  whatever drives ask, not to ask.
- **This file (`AGENTS.md`)** — build-side code-facts and conventions: how to develop and
  test ask. Not *operating policy* (push cadence, branch/merge workflow, isolation) — that
  depends on the agent setup and lives with the operator's workflow, not a contributor guide.
- **`README.md`** — what ask is and how to install it, for a new adopter.
- **`docs/spec.md`** — the authoritative implementation contract: field types, error shapes,
  exit codes, JSON shapes. Read it when implementing anything that touches the wire format.

## Project specifics

- **The binary** builds to `./bin/ask` (gitignored); rebuild with
  `go build -o bin/ask ./cmd/ask`. The binaries that ship with the plugin are produced by
  the release pipeline (CI cross-compiles and bundles them at tag time), never committed.
- **MCP is implemented in-tree** (no external library), following the in-binary server
  pattern — the MCP tools mirror the CLI one-to-one (`ask_new`, `ask_list`, …).
- **This repo dogfoods ask on its own backlog.** Mid-flight discoveries about ask itself are
  common; file them and keep working — that's the dogfood signal.
- **The skill tree is `go:embed`-ed** via `skills/embed.go` (embeds `skills/ask/`), and
  `ask install-skill` writes it to the host for non-plugin installs. There is exactly one
  canonical skill tree; do not add a second copy.

## Pre-commit gate

Before you commit, run — and keep green — the same checks the close gate enforces:

```sh
gofmt -l .        # must print nothing
go vet ./...
go test ./...
```

`scripts/smoke-external-dep.sh` is the cross-tool end-to-end smoke (the surface-via-report
protocol with the sibling `act` tool); run it after rebuilding either tool to confirm the
handshake still holds.

## Releasing

Releases are cut by tagging `vX.Y.Z`; the release pipeline (the shared `plugin-release-kit`
reusable workflow, pinned to an immutable commit SHA in `.github/workflows/release.yml`)
builds the binary tarballs and the self-contained plugin zip and attaches them to a draft
GitHub Release. The version's single source of truth is `metadata.version` in
`skills/ask/SKILL.md`, kept in lockstep across the manifests.
