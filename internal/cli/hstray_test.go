package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// TestHstrayHarvestAcceptance is act-f9ab23's acceptance: a target store
// with a hand-placed ASK-AAAB.json and a source holding ask-aaab. `ask
// harvest`, with and without --clean, exits non-zero naming the stray
// file and copies and deletes nothing. The refusal comes from comparing
// names, so it is the same on a case-sensitive filesystem.
func TestHstrayHarvestAcceptance(t *testing.T) {
	tmp := t.TempDir()
	src, tgt := filepath.Join(tmp, "src"), filepath.Join(tmp, "tgt")
	for _, d := range []string{src, tgt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	harvestSeedAt(t, src, "01HXWORK0000000000000000B", core.NewItem("ask-aaab", "source's",
		core.UrgencyNormal, core.StatusOpen, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)))
	harvestSeedAt(t, tgt, "01HXMAIN0000000000000000A")
	items := filepath.Join(tgt, ".ask", "items")
	if err := os.MkdirAll(items, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(items, "ASK-AAAB.json"), []byte("hand-placed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := idguardTree(t, tmp)

	inDir(t, tgt)
	for _, args := range [][]string{
		{"harvest", "--from", src},
		{"harvest", "--from", src, "--clean"},
	} {
		code, stdout, stderr := idguardRun(t, args...)
		if code != 5 {
			t.Errorf("%v: exit %d, want 5 (stderr %q)", args, code, stderr)
		}
		if !strings.Contains(stderr, "ASK-AAAB.json") || !strings.Contains(stderr, "ask-aaab") {
			t.Errorf("%v: stderr must name the stray file and the id, got %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: stdout must be empty on failure, got %q", args, stdout)
		}
		idguardSameTree(t, before, idguardTree(t, tmp))
	}
}
