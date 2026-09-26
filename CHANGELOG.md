# Changelog

All notable changes to `ask` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `ask version` distinguishes builds. Without an `-ldflags` stamp it now prints
  `dev+<commit>[.dirty] <commit time>` from the build info Go embeds in every build from a
  checkout, instead of the bare `dev` — so a stale install is visible from the binary itself.
  Release builds still print their stamp; MCP `serverInfo.version` reports the same string.
- `ask new` warns (never refuses) when the title or body uses relative-date language —
  `tomorrow`, `next week`, `in 3 days`, a bare `Monday` — and names the absolute date it
  resolves to at filing time, e.g. `"Tomorrow" ... (Tomorrow = 2026-09-26)`. Asks are read
  long after they are filed; "Tomorrow: run the dogfood" once sat for 63 days.
- `ask update <id>` corrects an item's text in place: `--title`, `--body`,
  `--body-file <path|->`, `--body-append`, `--body-append-file <path|->`. Flag names
  mirror `act update` so the sibling tools stay learnable together. It makes no state
  transition — an open ask stays open while its wording is fixed — which removes the two
  bad workarounds for a rotted ask: editing `.ask/items/<id>.json` by hand, and
  resolve-and-refile (which closes something nobody has done). Requesting text the item
  already has is an idempotent no-op: exit 6, no write.
- Exit code **7**, "stranded store": `.ask/config.json` is absent but `.ask/items/` holds
  item files. Previously indistinguishable from an uninitialized directory (both exit 5,
  same message), so an inbox full of open asks could be swept up and counted as zero.
  Surfaced on the CLI and in the MCP error envelope; `ask init` in that directory is the
  repair and adopts the existing items.

### Changed
- The skill's filing rules now carry a **has-agent-arm check**: before filing, state why no
  agent in reach can take the action. "I can't do this from here" is not "no agent can do
  this" — another machine may hold the credential, the tool, or the network path — and a body
  containing a runnable command is the signature of an item that belongs in a tracker, not in
  a human's inbox.

### Fixed
- A failed read no longer mutates the filesystem. `OpenStore` created `.ask/items/` before
  checking for `config.json`, so `ask list` in a directory with no store created a
  store-shaped husk there and *then* errored. Those husks are indistinguishable from real
  stores to anything scanning for `.ask` directories, so one stray read permanently
  enrolled a repo in every future sweep. `.ask/` is now created only by `ask init`, and
  `.ask/items/` only by the first item write.
- `--body-file -` / `--body-append-file -` (and any future `-`-valued flag) now parse
  correctly when the flag precedes the id; the flag reorderer treated a bare `-` as the
  start of another flag and swallowed the id as the flag's value.

## [0.2.1]

### Fixed
- MCP `serverInfo.version` now reads the stamped `internal/version.Binary` (the same
  source as `ask version`) instead of a hardcoded `0.1.0` literal, so it no longer
  drifts from the release version. verify-release check 7 gates this going forward.

## [0.2.0]

### Removed
- `ask install-skill` command and the in-binary skill `go:embed`. Skill delivery is now
  plugin-first: `/plugin install ask@ask` ships and auto-discovers the skill. The bare
  binary is CLI-only; a source install (`install.sh`) copies `skills/ask/` from the
  checkout, and non-plugin users can copy it from a repo clone.

## [0.1.0]

### Added

- Initial public release: single Go binary with an embedded skill and in-process MCP server.
- Self-contained plugin (per-repo marketplace) for Claude Code, Cowork / Claude Desktop, and Codex — the canonical install; the bundled binary runs with no separate setup.
- Non-plugin setup via `ask install-skill` (writes the skill to the host) and `ask mcp` (MCP server).
- Per-arch binaries (darwin / linux × amd64 / arm64) + uname launcher committed into the plugin and built by CI under the commit-to-main release model.
