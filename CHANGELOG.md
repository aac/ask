# Changelog

All notable changes to `ask` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
