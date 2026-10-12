package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// CLI half of act-16ad85. The fixture is the ticket's: a store holding
// ask-aaaa.json and a stray ASK-AAAB.json (a pairing a case-insensitive
// filesystem can hold too).

// strayStore seeds a store at root with ask-aaaa and a hand-placed
// ASK-AAAB.json.
func strayStore(t *testing.T, root, projectID string) {
	t.Helper()
	harvestSeedAt(t, root, projectID, core.NewItem("ask-aaaa", "the real one",
		core.UrgencyNormal, core.StatusOpen, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)))
	b := []byte(`{"schema_version":"1","id":"ASK-AAAB","title":"stray","status":"open","urgency":"normal","created_at":"2026-05-15T10:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(root, ".ask", "items", "ASK-AAAB.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStrayShowAcceptance is the ticket's acceptance: `ask show aaab`
// exits 5 with a message naming the stray file, `ask show aaaa` exits 0.
// A prefix the stray shares with the real item resolves to the real item.
func TestStrayShowAcceptance(t *testing.T) {
	root := t.TempDir()
	strayStore(t, root, "01HXMAIN0000000000000000A")
	inDir(t, root)

	for _, arg := range []string{"aaab", "ask-aaab", "ASK-AAAB"} {
		code, stdout, stderr := idguardRun(t, "show", arg)
		if code != 5 {
			t.Errorf("show %s: exit %d, want 5 (stderr %q)", arg, code, stderr)
		}
		if !strings.Contains(stderr, "ASK-AAAB.json") {
			t.Errorf("show %s: stderr must name the stray file, got %q", arg, stderr)
		}
		if stdout != "" {
			t.Errorf("show %s: stdout must be empty on error, got %q", arg, stdout)
		}
	}

	for _, arg := range []string{"aaaa", "ask-aaaa", "aaa", "a"} {
		code, stdout, stderr := idguardRun(t, "show", arg)
		if code != 0 {
			t.Errorf("show %s: exit %d, want 0 (stderr %q)", arg, code, stderr)
		}
		if !strings.Contains(stdout, "ask-aaaa") || !strings.Contains(stdout, "the real one") {
			t.Errorf("show %s: want the real item, got %q", arg, stdout)
		}
	}

	// A write verb addressed by the shared prefix reaches the real item too.
	if code, _, stderr := idguardRun(t, "resolve", "aaa"); code != 0 {
		t.Errorf("resolve aaa: exit %d, want 0 (stderr %q)", code, stderr)
	}
}

// TestStrayHarvestNamesCollisionAndStray: one `ask harvest` run reports
// both the id collision and the stray source file, and copies nothing. The
// stray is named as a file, in the words list and show use (act-5f279f).
func TestStrayHarvestNamesCollisionAndStray(t *testing.T) {
	tmp := t.TempDir()
	src, tgt := filepath.Join(tmp, "src"), filepath.Join(tmp, "tgt")
	for _, d := range []string{src, tgt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	strayStore(t, src, "01HXWORK0000000000000000B")
	harvestSeedAt(t, tgt, "01HXMAIN0000000000000000A", core.NewItem("ask-aaaa", "target's",
		core.UrgencyNormal, core.StatusOpen, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)))
	before := idguardTree(t, tmp)

	inDir(t, tgt)
	code, stdout, stderr := idguardRun(t, "harvest", "--from", src, "--clean")
	if code != 5 {
		t.Errorf("harvest: exit %d, want 5 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "collision") || !strings.Contains(stderr, "ask-aaaa") ||
		!strings.Contains(stderr, "stray file in .ask/items/ not named for an id: ASK-AAAB.json") {
		t.Errorf("harvest: stderr must name the collision and the stray, got %q", stderr)
	}
	if stdout != "" {
		t.Errorf("harvest: stdout must be empty on failure, got %q", stdout)
	}
	idguardSameTree(t, before, idguardTree(t, tmp))
}
