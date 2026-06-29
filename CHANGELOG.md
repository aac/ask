# Changelog

All notable changes to `ask` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0]

### Added

- Initial public release: single Go binary with an embedded skill and in-process MCP server.
- Self-contained plugin (per-repo marketplace) for Claude Code, Cowork / Claude Desktop, and Codex — the canonical install; the bundled binary runs with no separate setup.
- Non-plugin setup via `ask install-skill` (writes the skill to the host) and `ask mcp` (MCP server).
- Cross-compiled binary tarballs (darwin / linux × amd64 / arm64) attached to GitHub Releases for direct install.

[Unreleased]: https://github.com/aac/ask/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/aac/ask/releases/tag/v0.1.0
