// Package version exposes the ask binary's build-time version string.
// The value is "dev" in source and is overridden at link time via
// -ldflags -X github.com/aac/ask/internal/version.Binary=<tag> by both the
// Makefile and GoReleaser.
//
// This package is the single source of the binary's version, shared by every
// surface that reports it — internal/cli (`ask version`) and internal/mcp (the
// MCP initialize response's serverInfo.version) — without a circular import.
package version

// Binary is the ask binary version, injected via -ldflags at build time.
// Format: vX.Y.Z+abc1234 (semver + short SHA), per docs/distribution-readiness.md §4.
// Defaults to "dev" for source builds.
var Binary = "dev"
