// Package version exposes the ask binary's build-time version string.
// Binary is "dev" in source and is overridden at link time via
// -ldflags -X github.com/aac/ask/internal/version.Binary=<tag> by both the
// Makefile and GoReleaser. When it is not overridden, String() falls back to
// the VCS stamp the Go toolchain embeds in every build from a git checkout
// (vcs.revision, vcs.time, vcs.modified), so a plain `go install ./cmd/ask`
// still reports which commit it was built from.
//
// This package is the single source of the binary's version, shared by every
// surface that reports it — internal/cli (`ask version`) and internal/mcp (the
// MCP initialize response's serverInfo.version) — without a circular import.
package version

import (
	"runtime/debug"
	"strings"
)

// Binary is the ask binary version, injected via -ldflags at build time.
// Format: vX.Y.Z+abc1234 (semver + short SHA), per docs/distribution-readiness.md §4.
// Defaults to "dev" for source builds.
var Binary = "dev"

// String returns the version every surface should report: the -ldflags
// stamp when one was injected, otherwise a string built from the embedded
// build info, otherwise the bare Binary default.
func String() string {
	bi, _ := debug.ReadBuildInfo()
	return format(Binary, bi)
}

// format is String with its inputs made explicit, for tests.
//
// With no ldflags stamp it yields, in order of preference:
//   - the module version when installed as `go install …@vX.Y.Z` (but not a
//     pseudo-version of the commit already named by the VCS stamp);
//   - "dev+<12-char revision>[.dirty] <commit time>" from the VCS stamp;
//   - Binary unchanged when the build carries neither (e.g. -buildvcs=false).
func format(binary string, bi *debug.BuildInfo) string {
	if binary != "dev" || bi == nil {
		return binary
	}
	var rev, when string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	// A real module version (`go install …@v0.3.0`) wins. Since Go 1.24 a
	// build from a checkout also fills Main.Version, with a pseudo-version
	// derived from the same commit; the explicit form below reads better.
	if v := bi.Main.Version; v != "" && v != "(devel)" && (rev == "" || !strings.Contains(v, rev)) {
		return v
	}
	if rev == "" {
		return binary
	}
	var b strings.Builder
	b.WriteString(binary + "+" + rev)
	if modified {
		b.WriteString(".dirty")
	}
	if when != "" {
		b.WriteString(" " + when)
	}
	return b.String()
}
