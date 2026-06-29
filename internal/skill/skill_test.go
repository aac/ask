package skill

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/aac/ask/internal/version"
)

// TestSkillMDStampsVersion: SkillMD() rewrites the `version:` line of
// the embedded SKILL.md to internal/version.Binary. With the default
// "dev" value this should still match (since the source carries
// `version: dev`); the meaningful assertion is that the line is exactly
// `version: <version.Binary>` regardless of what the source carries.
func TestSkillMDStampsVersion(t *testing.T) {
	got, err := SkillMD()
	if err != nil {
		t.Fatalf("SkillMD: %v", err)
	}
	wantLine := []byte("\nversion: " + version.Binary + "\n")
	if !bytes.Contains(got, wantLine) {
		t.Errorf("SkillMD missing stamped version line %q\nfirst 300 bytes:\n%s",
			string(wantLine), truncate(got, 300))
	}
}

// TestFSReturnsStampedSkillMD: fs.ReadFile against FS() returns the
// stamped SKILL.md, mirroring SkillMD(). install-skill walks FS() so
// the two paths must agree.
func TestFSReturnsStampedSkillMD(t *testing.T) {
	got, err := fs.ReadFile(FS(), "SKILL.md")
	if err != nil {
		t.Fatalf("fs.ReadFile: %v", err)
	}
	via, err := SkillMD()
	if err != nil {
		t.Fatalf("SkillMD: %v", err)
	}
	if !bytes.Equal(got, via) {
		t.Errorf("FS() SKILL.md differs from SkillMD()\nFS first 200:\n%s\nSkillMD first 200:\n%s",
			truncate(got, 200), truncate(via, 200))
	}
}

// TestFSReferenceFilesUntouched: reference files come back verbatim
// from the embed — only SKILL.md is rewritten.
func TestFSReferenceFilesUntouched(t *testing.T) {
	entries, err := fs.ReadDir(FS(), "references")
	if err != nil {
		t.Fatalf("read references dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("references dir is empty in embed")
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		viaFS, err := fs.ReadFile(FS(), "references/"+e.Name())
		if err != nil {
			t.Fatalf("fs.ReadFile via FS: %v", err)
		}
		// The raw embed.FS is rooted at "ask/..." (see skill.go for
		// why); FS() exposes a subtree rooted below that. Compare via
		// the raw path here to confirm FS() forwards reference files
		// verbatim from the underlying embed.
		viaEmbed, err := files.ReadFile(skillRoot + "/references/" + e.Name())
		if err != nil {
			t.Fatalf("read embed direct: %v", err)
		}
		if !bytes.Equal(viaFS, viaEmbed) {
			t.Errorf("references/%s differs through FS() vs raw embed", e.Name())
		}
	}
}

// TestStampVersionReplacesExistingLine: source carries `version: dev`,
// stamp rewrites it to the supplied value, preserves the rest of the
// frontmatter and body byte-for-byte.
func TestStampVersionReplacesExistingLine(t *testing.T) {
	src := []byte("---\nname: ask\nversion: dev\ndescription: foo\n---\n\nbody\n")
	got := stampVersion(src, "v1.2.3+abc1234")
	want := []byte("---\nname: ask\nversion: v1.2.3+abc1234\ndescription: foo\n---\n\nbody\n")
	if !bytes.Equal(got, want) {
		t.Errorf("stampVersion mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestStampVersionNoVersionLineIsNoop: a frontmatter without a
// `version:` line is returned unchanged — we don't invent one.
func TestStampVersionNoVersionLineIsNoop(t *testing.T) {
	src := []byte("---\nname: ask\ndescription: foo\n---\n\nbody\n")
	got := stampVersion(src, "v9.9.9")
	if !bytes.Equal(got, src) {
		t.Errorf("expected no-op when version line missing; got %q", got)
	}
}

// TestStampVersionNoFrontmatterIsNoop: a file without a `---` opener is
// returned unchanged.
func TestStampVersionNoFrontmatterIsNoop(t *testing.T) {
	src := []byte("just markdown\nversion: not-frontmatter\n")
	got := stampVersion(src, "v9.9.9")
	if !bytes.Equal(got, src) {
		t.Errorf("expected no-op without frontmatter; got %q", got)
	}
}

// TestStampVersionOnlyTouchesFrontmatter: a `version:` line in the body
// (after the closing fence) is left alone.
func TestStampVersionOnlyTouchesFrontmatter(t *testing.T) {
	src := []byte("---\nname: ask\nversion: dev\n---\n\nbody version: still-here\n")
	got := stampVersion(src, "v1.0.0")
	if !strings.Contains(string(got), "body version: still-here") {
		t.Errorf("body version line was rewritten; got %q", got)
	}
	if !strings.Contains(string(got), "version: v1.0.0\n") {
		t.Errorf("frontmatter version line not rewritten; got %q", got)
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
