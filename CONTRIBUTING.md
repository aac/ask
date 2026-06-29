# Contributing to ask

Thanks for the interest. `ask` is pre-v1 but battle-tested in daily use, and the
design has converged — see [`docs/brief.md`](docs/brief.md) before proposing
changes to behavior, and [`docs/spec.md`](docs/spec.md) before implementing.

## Build and test

```sh
make build      # go build ./...
make test       # go test ./...
make lint       # go vet ./...
make fmt        # gofmt -w .
```

For a full release-target smoke without cutting a tag:

```sh
make release-local      # builds the 5-target matrix into ./dist
```

## Quality gate

Before committing, run the three checks that make up the repo's quality bar:

```sh
gofmt -l .              # must print nothing
go vet ./...
go test ./...
```

`gofmt -l .` must be empty, `go vet ./...` must be clean, and `go test ./...`
must be green.

## Cross-tool integration test (ask + act)

`ask` interoperates with its sibling tracker [`act`](https://github.com/aac/act):
an ask can record which tracked task is waiting on it, and act can mark that task
blocked until the ask resolves (see `docs/brief.md` → Blocker-handling protocol).
`scripts/smoke-external-dep.sh` is the end-to-end test for that handshake. It
exercises both binaries against an isolated tempdir: files an ask with
`--blocks <act-id>`, registers the matching block on the act side with
`act update --ext-add`, asserts `act ready` excludes the blocked issue, resolves
the ask, clears the dependency, and asserts the issue returns to `act ready`. The
script self-cleans on exit, makes no network calls, and fails fast with a named
step on any drift.

Run it after rebuilding either `ask` or `act` to confirm the handshake still holds:

```sh
scripts/smoke-external-dep.sh
```

It exits `0` on success, `2` with a "missing feature" diagnostic if a flag is gone,
and `1` on assertion failure with the failing step named.

## Commits

Write a short, descriptive subject line and explain the "why" in the body when
it isn't obvious, e.g.:

```
core: add schema_version field to Item
```

You may notice an `Act-Id: act-XXXX` trailer in some commits. This repo's
maintainer uses [act](https://github.com/aac/act) for task tracking, and that
trailer pairs a commit with its tracked issue. You don't need to add it — the
trailer is ignored by conventional-commit linters, semantic-release, and
changelog generators, and has no effect on merge or review.

## Branch policy

Develop on a feature branch and open a pull request against `main`; direct
commits to `main` are blocked by a `.githooks/commit-msg` hook. After cloning,
enable the repo's hooks once:

```sh
git config --local core.hooksPath .githooks
```

## Filing issues

Bugs, feature requests, and questions go to a GitHub issue — see
[`.github/ISSUE_TEMPLATE/`](.github/ISSUE_TEMPLATE/).

## Code of conduct

Be decent. Don't burn anyone's time. That's the whole rule.
