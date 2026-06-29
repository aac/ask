// Package skills owns the //go:embed directive that bakes the
// canonical ask skill tree (SKILL.md + references/) into the binary.
//
// The skill source lives at skills/ask/ so the same on-disk tree is
// consumed by:
//
//   - Codex plugin discovery, which walks the filesystem under a plugin
//     root and expects a conventional skills/<name>/ layout, and
//   - the ask binary itself, via the embed directive below.
//
// Go's //go:embed directive cannot use `..` to escape the package
// directory, so the embed must live in a package whose own directory
// is at or above the embedded tree. This package sits at skills/ —
// the immediate parent of skills/ask/ — so the embed directive can use
// the relative path `ask/SKILL.md` and `ask/references/*.md` directly.
//
// The richer skill API (version stamping, FS overlay, install-skill
// integration) lives in internal/skill; this package only provides the
// raw bytes via an exported embed.FS.
package skills

import "embed"

// FS is the raw embedded skill tree. Files are exposed under their
// path relative to this package directory, i.e. "ask/SKILL.md" and
// "ask/references/<name>.md".
//
//go:embed ask/SKILL.md ask/references/*.md
var FS embed.FS
