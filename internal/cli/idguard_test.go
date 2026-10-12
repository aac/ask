package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// CLI half of act-7ee74d: an item file whose "id" does not equal its
// filename stem is unreadable (exit 5), and harvest can neither write nor
// delete through it. Every fixture lives under t.TempDir().

// idguardPlant seeds a store at root (created if needed) with one item
// file per fileID→contentID pair: the file ask itself writes, with only
// the "id" member rewritten.
func idguardPlant(t *testing.T, root, projectID string, ids map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for fileID, contentID := range ids {
		harvestSeedAt(t, root, projectID,
			core.NewItem(fileID, "planted "+fileID, core.UrgencyNormal, core.StatusOpen, base))
		p := filepath.Join(root, ".ask", "items", fileID+".json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		from := []byte(`"id": "` + fileID + `"`)
		if !bytes.Contains(b, from) {
			t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
		}
		if err := os.WriteFile(p, bytes.Replace(b, from, []byte(`"id": "`+contentID+`"`), 1), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// idguardTree returns relative path → bytes for every file under root.
func idguardTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		tree[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tree
}

// idguardSameTree fails when the two trees differ in file set or bytes.
func idguardSameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	for name, b := range before {
		a, ok := after[name]
		if !ok {
			t.Errorf("%s was removed", name)
		} else if a != b {
			t.Errorf("%s was rewritten:\n%s", name, a)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Errorf("%s was created", name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

// idguardRun runs one ask invocation, returning exit code, stdout, stderr.
func idguardRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { code = Run(args) })
	})
	return code, stdout, stderr
}

// TestIdguardAcceptance is the ticket's scenario verbatim: source
// ask-aaaa.json carries "id": "../../victim", with a victim.json two
// levels above items/ in both stores. `harvest --clean` exits 5 naming
// the mismatch, both victims are byte-identical, nothing new exists in
// the target, the source file is still there; `show ask-aaaa` in the
// source also exits 5.
func TestIdguardAcceptance(t *testing.T) {
	tmp := t.TempDir()
	src, tgt := filepath.Join(tmp, "src"), filepath.Join(tmp, "tgt")
	idguardPlant(t, src, "01HXWORK0000000000000000B", map[string]string{"ask-aaaa": "../../victim"})
	idguardPlant(t, tgt, "01HXMAIN0000000000000000A", nil)
	harvestSeedAt(t, tgt, "01HXMAIN0000000000000000A")
	for _, d := range []string{src, tgt} {
		if err := os.WriteFile(filepath.Join(d, "victim.json"), []byte("victim of "+d+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := idguardTree(t, tmp)
	for _, want := range []string{"src/victim.json", "tgt/victim.json", "src/.ask/items/ask-aaaa.json"} {
		if _, ok := before[filepath.FromSlash(want)]; !ok {
			t.Fatalf("fixture premise broken: %s missing from %v", want, before)
		}
	}

	inDir(t, tgt)
	code, stdout, stderr := idguardRun(t, "harvest", "--from", src, "--clean")
	if code != 5 {
		t.Errorf("harvest: exit %d, want 5 (stdout %q stderr %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "ask-aaaa") || !strings.Contains(stderr, "../../victim") ||
		!strings.Contains(stderr, "does not match") {
		t.Errorf("harvest: stderr must name the id mismatch and both ids, got %q", stderr)
	}
	if stdout != "" {
		t.Errorf("harvest: stdout must be empty on failure, got %q", stdout)
	}
	idguardSameTree(t, before, idguardTree(t, tmp))

	inDir(t, src)
	for _, args := range [][]string{{"show", "ask-aaaa"}, {"show", "--json", "ask-aaaa"}} {
		code, stdout, stderr := idguardRun(t, args...)
		if code != 5 {
			t.Errorf("%v: exit %d, want 5 (stderr %q)", args, code, stderr)
		}
		if !strings.Contains(stderr, "ask-aaaa") || !strings.Contains(stderr, "../../victim") {
			t.Errorf("%v: stderr must name both ids, got %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: stdout must be empty on error, got %q", args, stdout)
		}
	}
	idguardSameTree(t, before, idguardTree(t, tmp))
}

// TestIdguardHarvestCannotOverwriteTargetItem: ask-aaaa.json claims to be
// ask-bbbb, which the target already holds. The filename-based collision
// preflight does not see it; harvest must still exit 5 and leave the
// target's ask-bbbb byte-identical.
func TestIdguardHarvestCannotOverwriteTargetItem(t *testing.T) {
	tmp := t.TempDir()
	src, tgt := filepath.Join(tmp, "src"), filepath.Join(tmp, "tgt")
	idguardPlant(t, src, "01HXWORK0000000000000000B", map[string]string{"ask-aaaa": "ask-bbbb"})
	idguardPlant(t, tgt, "01HXMAIN0000000000000000A", map[string]string{"ask-bbbb": "ask-bbbb"})
	before := idguardTree(t, tmp)
	inDir(t, tgt)
	for _, args := range [][]string{
		{"harvest", "--from", src},
		{"harvest", "--from", src, "--clean"},
	} {
		code, _, stderr := idguardRun(t, args...)
		if code != 5 {
			t.Errorf("%v: exit %d, want 5 (stderr %q)", args, code, stderr)
		}
		if !strings.Contains(stderr, "ask-aaaa") || !strings.Contains(stderr, "ask-bbbb") {
			t.Errorf("%v: stderr must name both ids, got %q", args, stderr)
		}
		idguardSameTree(t, before, idguardTree(t, tmp))
	}
}

// TestIdguardListSkipsAndWriteVerbsRefuse: `ask list` skips the
// mismatched file with the per-id warning and still lists its sibling;
// every verb addressed at it exits 5 and leaves it byte-identical.
func TestIdguardListSkipsAndWriteVerbsRefuse(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	idguardPlant(t, src, "01HXWORK0000000000000000B",
		map[string]string{"ask-aaaa": "ask-bbbb", "ask-cccc": "ask-cccc"})
	before := idguardTree(t, tmp)
	inDir(t, src)

	code, stdout, stderr := idguardRun(t, "list")
	if code != 0 {
		t.Fatalf("list: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "ask-cccc") || strings.Contains(stdout, "ask-aaaa") || strings.Contains(stdout, "ask-bbbb") {
		t.Fatalf("list: stdout should hold only ask-cccc, got %q", stdout)
	}
	if !strings.Contains(stderr, "ask list: skipped ask-aaaa:") || !strings.Contains(stderr, "ask-bbbb") {
		t.Fatalf("list: want a skip warning for ask-aaaa naming ask-bbbb, got %q", stderr)
	}

	for _, args := range [][]string{
		{"resolve", "ask-aaaa"},
		{"close", "ask-aaaa"},
		{"reopen", "ask-aaaa"},
		{"update", "ask-aaaa", "--body", "rewritten"},
	} {
		code, _, stderr := idguardRun(t, args...)
		if code != 5 {
			t.Errorf("%v: exit %d, want 5 (stderr %q)", args, code, stderr)
		}
		idguardSameTree(t, before, idguardTree(t, tmp))
	}
	// The id the file falsely claims does not exist as a file: not found.
	if code, _, _ := idguardRun(t, "show", "ask-bbbb"); code != 3 {
		t.Errorf("show ask-bbbb: exit %d, want 3", code)
	}
	// User-typed prefixes keep resolving against filenames.
	if code, _, stderr := idguardRun(t, "show", "ask-c"); code != 0 {
		t.Errorf("show ask-c: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if code, _, _ := idguardRun(t, "show", "../../victim"); code != 3 {
		t.Errorf("show ../../victim: exit %d, want 3", code)
	}
}
