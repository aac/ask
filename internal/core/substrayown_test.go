package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-7c6b02: a file in a store's OWN .ask/subs/ whose
// name differs from an item's subs file only in case (ASK-AAAB.json
// against ask-aaab.json). Reproduced on macOS before the fix: `ask show
// aaab` printed the hand-placed file's subscriptions as ask-aaab's, and
// Subscribe wrote the new subscription into it, leaving it named
// ASK-AAAB.json. The store now refuses by comparing names, so the same
// store is refused on a case-sensitive filesystem too.

// substrayOwnPlant hand-places, under name, a well-formed subs file for
// ask-aaab holding one subscription, so a read that lands on it succeeds.
func substrayOwnPlant(t *testing.T, s *FileStore, name string) {
	t.Helper()
	dir := filepath.Join(s.Root(), subsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"1","item":"ask-aaab","subscriptions":[{"id":"deadbeef",` +
		`"events":["close"],"handler":"planted","args":{},"added_at":"2026-05-15T11:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// substrayOwnRefused fails unless err is the stray refusal naming the
// file and the item.
func substrayOwnRefused(t *testing.T, what, stray string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s beside subs/%s: want a refusal naming the stray, got no error", what, stray)
	}
	if !strings.Contains(err.Error(), "subs/"+stray) || !strings.Contains(err.Error(), "ask-aaab") {
		t.Errorf("%s: error must name subs/%s and ask-aaab, got %q", what, stray, err)
	}
}

func TestSubstrayOwnStoreRefusesCaseTwin(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for _, stray := range []string{"ASK-AAAB.json", "ask-aaab.JSON", "Ask-aaab.json"} {
		s := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
			NewItem("ask-aaab", "has a stray", UrgencyNormal, StatusOpen, base),
			NewItem("ask-cccc", "bystander", UrgencyNormal, StatusOpen, base))
		substrayOwnPlant(t, s, stray)
		before := substrayTree(t, s)

		f, err := s.LoadSubs("ask-aaab")
		substrayOwnRefused(t, "LoadSubs", stray, err)
		if f != nil {
			t.Errorf("%s: LoadSubs returned the stray's subscriptions: %+v", stray, f)
		}
		_, err = s.Subscribe("ask-aaab", []string{EventResolve}, "h", nil, base)
		substrayOwnRefused(t, "Subscribe", stray, err)
		_, _, err = s.Unsubscribe("ask-aaab", "deadbeef")
		substrayOwnRefused(t, "Unsubscribe", stray, err)
		substraySame(t, stray, before, substrayTree(t, s))

		// The stray is in the way of one item only.
		if f, err := s.LoadSubs("ask-cccc"); err != nil || len(f.Subscriptions) != 0 {
			t.Errorf("%s: bystander: %+v, %v", stray, f, err)
		}
		substraySubscribe(t, s, "ask-cccc")
	}
}
