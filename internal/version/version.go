// Package version exposes the ask binary's build-time version string.
// The value is "dev" in source and is overridden at link time via
// -ldflags -X github.com/aac/ask/internal/version.Binary=<tag> by both the
// Makefile and GoReleaser.
//
// This package exists so internal/skill (which injects the version into
// the embedded SKILL.md frontmatter on read) and internal/cli (which
// prints the version from `ask version` and surfaces it from
// install-skill) can share a single source of truth without a circular
// import.
package version

// Binary is the ask binary version, injected via -ldflags at build time.
// Format: vX.Y.Z+abc1234 (semver + short SHA), per docs/distribution-readiness.md §4.
// Defaults to "dev" for source builds.
var Binary = "dev"
