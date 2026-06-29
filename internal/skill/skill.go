// Package skill embeds the canonical ask skill file (and its reference
// docs) at build time so the ask binary can install them into a user's
// Claude Code skills directory via `ask install-skill`.
//
// The skill is the runtime workflow document agents read whenever they
// land in a repo using ask. The canonical copy lives at
// ~/.claude/skills/ask/SKILL.md, which is not itself under version
// control. Embedding it in the binary makes the ask repo the single
// source of truth: edits land here, ship with the next release, and
// install-skill propagates them to every machine that has the binary.
//
// The embedded file set mirrors the on-disk skill layout exactly:
//
//	SKILL.md                           # required
//	references/*.md                    # progressive-disclosure docs
//
// New reference files added to ./references/ are automatically included
// in the embed because the directive uses a glob.
//
// # SKILL.md frontmatter version stamping
//
// The on-disk SKILL.md carries the literal placeholder `version: dev` in
// its YAML frontmatter so the source tree is stable (no per-commit churn
// from a stamping pass). The actual binary version is injected at read
// time by SkillMD() and by the FS() walker used by install-skill: the
// `version:` line is rewritten to the value of internal/version.Binary,
// which is itself populated by -ldflags at build time. Result: an
// installed SKILL.md always self-identifies which ask binary shipped it
// (`head -5 SKILL.md`), while the source tree never changes when a new
// version is cut.
package skill

import (
	"bytes"
	"io/fs"
	"testing/fstest"
	"time"

	"github.com/aac/ask/internal/version"
	skillsembed "github.com/aac/ask/skills"
)

// The canonical skill source lives at repo-root skills/ask/ so that
// both Go embed (baked into the binary) and Codex plugin discovery
// (filesystem walk under the plugin root) consume the same tree.
//
// Go's //go:embed directive cannot escape its own package directory
// with `..`, so the actual embed.FS is owned by package skills at
// skills/embed.go (whose directory is the immediate parent of the
// skill tree). This loader package consumes that raw FS and layers on
// version stamping and an overlay-based FS view for install-skill.
//
// files is the raw embedded tree, with entries rooted at "ask/...".
var files = skillsembed.FS

// skillRoot is the prefix inside files where the skill tree lives.
// We expose an FS rooted *below* this so callers see the historic
// layout (SKILL.md and references/<file> at the top level).
const skillRoot = "ask"

// skillMDPath is the embed path to the top-level SKILL.md, in the
// underlying raw FS (used by SkillMD which reads files directly).
const skillMDPath = skillRoot + "/SKILL.md"

// FS returns the embedded skill file tree rooted at the skill
// directory (so callers see "SKILL.md" and "references/<file>" at the
// top level), with the SKILL.md frontmatter `version:` line rewritten
// to the running binary's version. Callers walk it with fs.WalkDir to
// copy each entry to the destination skills dir. The returned FS is
// read-only.
//
// Only SKILL.md is rewritten; reference files are returned verbatim.
func FS() fs.FS {
	base, subErr := fs.Sub(files, skillRoot)
	if subErr != nil {
		// Build-time invariant: the embed directive points at
		// skills/ask. If sub fails we have nothing useful to return.
		return files
	}
	stamped, err := SkillMD()
	if err != nil {
		// SkillMD only fails if the embed itself is missing SKILL.md,
		// which is a build-time invariant. Fall back to the raw subtree
		// so the caller still sees the (unstamped) source rather than
		// silently masking the file entirely.
		return base
	}
	overlay := fstest.MapFS{
		"SKILL.md": &fstest.MapFile{
			Data:    stamped,
			Mode:    0o644,
			ModTime: time.Time{},
		},
	}
	return mergedFS{base: base, overlay: overlay}
}

// SkillMD returns the bytes of the top-level SKILL.md with the
// frontmatter `version:` line rewritten to the running binary's version
// (internal/version.Binary). The on-disk source contains the literal
// `version: dev`; this function replaces it on read so the source tree
// stays stable while shipped binaries self-identify.
func SkillMD() ([]byte, error) {
	raw, err := files.ReadFile(skillMDPath)
	if err != nil {
		return nil, err
	}
	return stampVersion(raw, version.Binary), nil
}

// stampVersion rewrites the `version:` line inside the SKILL.md YAML
// frontmatter (the block between the first two `---` lines) to
// `version: <v>`. If no frontmatter or no `version:` line exists the
// input is returned unchanged.
//
// Exposed for testing.
func stampVersion(src []byte, v string) []byte {
	// Find the opening frontmatter fence: must be `---\n` at offset 0.
	const fence = "---\n"
	if !bytes.HasPrefix(src, []byte(fence)) {
		return src
	}
	// Find the closing fence after the opener.
	rest := src[len(fence):]
	closeIdx := bytes.Index(rest, []byte("\n"+fence[:3]+"\n"))
	if closeIdx < 0 {
		// Try with no trailing newline (EOF case).
		closeIdx = bytes.Index(rest, []byte("\n"+fence[:3]))
		if closeIdx < 0 {
			return src
		}
	}
	frontmatter := rest[:closeIdx]
	tail := rest[closeIdx:]

	lines := bytes.Split(frontmatter, []byte("\n"))
	found := false
	for i, line := range lines {
		// Match `version:` at the start of the line, ignoring leading
		// whitespace (frontmatter is flat YAML in our schema, but be
		// forgiving).
		trimmed := bytes.TrimLeft(line, " \t")
		if bytes.HasPrefix(trimmed, []byte("version:")) {
			lines[i] = []byte("version: " + v)
			found = true
			break
		}
	}
	if !found {
		return src
	}
	newFront := bytes.Join(lines, []byte("\n"))

	out := make([]byte, 0, len(src))
	out = append(out, fence...)
	out = append(out, newFront...)
	out = append(out, tail...)
	return out
}

// mergedFS lets us serve a small set of rewritten files (overlay) on top
// of the embed.FS (base) without copying the entire embed into memory.
// Lookups consult overlay first; everything else falls through to base.
type mergedFS struct {
	base    fs.FS
	overlay fstest.MapFS
}

func (m mergedFS) Open(name string) (fs.File, error) {
	if _, ok := m.overlay[name]; ok {
		return m.overlay.Open(name)
	}
	return m.base.Open(name)
}

func (m mergedFS) ReadFile(name string) ([]byte, error) {
	if _, ok := m.overlay[name]; ok {
		return m.overlay.ReadFile(name)
	}
	return fs.ReadFile(m.base, name)
}

func (m mergedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	// Directory listings come from the base FS — the overlay only
	// rewrites file contents, it doesn't introduce new entries.
	return fs.ReadDir(m.base, name)
}
