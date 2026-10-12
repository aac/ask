package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// TestSubstrayOwnAcceptance is act-7c6b02's acceptance: a store with
// ask-aaab and a hand-placed .ask/subs/ASK-AAAB.json. `ask subscribe` on
// aaab and `ask show aaab` exit 5 naming the stray file, and nothing in
// the store changes. The refusal comes from comparing names, so it is the
// same on a case-sensitive filesystem. `ask show --json` is the item
// alone and never reads subs/, so it is unaffected.
func TestSubstrayOwnAcceptance(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "h")
	dir := t.TempDir()
	inDir(t, dir)
	seedStore(t, dir, core.NewItem("ask-aaab", "has a stray", core.UrgencyNormal, core.StatusOpen,
		time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)))
	subsDir := filepath.Join(dir, ".ask", "subs")
	if err := os.MkdirAll(subsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := `{"schema_version":"1","item":"ask-aaab","subscriptions":[{"id":"deadbeef",` +
		`"events":["close"],"handler":"planted","args":{},"added_at":"2026-05-15T11:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(subsDir, "ASK-AAAB.json"), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	before := idguardTree(t, dir)

	for _, args := range [][]string{
		{"subscribe", "aaab", "--on", "resolve", "--handler", "h"},
		{"show", "aaab"},
		{"unsubscribe", "aaab", "deadbeef"},
	} {
		code, stdout, stderr := subsRun(t, args...)
		if code != 5 {
			t.Errorf("%v: exit %d, want 5\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
		}
		if !strings.Contains(stderr, "subs/ASK-AAAB.json") || !strings.Contains(stderr, "ask-aaab") {
			t.Errorf("%v: stderr must name the stray file and the id, got %q", args, stderr)
		}
		if strings.Contains(stdout, "planted") || strings.Contains(stdout, "deadbeef") {
			t.Errorf("%v: stdout shows the stray's subscription: %q", args, stdout)
		}
	}
	after := idguardTree(t, dir)
	if len(before) != len(after) {
		t.Errorf("store files changed: before %d, after %d", len(before), len(after))
	}
	for name, b := range before {
		if a, ok := after[name]; !ok || a != b {
			t.Errorf("%s changed or missing", name)
		}
	}

	if code, stdout, stderr := subsRun(t, "show", "aaab", "--json"); code != 0 || !strings.Contains(stdout, `"ask-aaab"`) {
		t.Errorf("show --json: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
