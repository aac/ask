package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-320c31, two consequences of act-7c6b02 that were
// checked by hand only. Both start from a store whose own .ask/subs/
// holds a case-variant of an item's subs file (ASK-AAAB.json for
// ask-aaab) and no real one. The refusal compares names, so the setup
// behaves the same on a case-sensitive filesystem, where the stray is
// simply a differently-named file.

// substraypinPlant hand-places, under name, a well-formed subs file for
// ask-aaab holding one subscription on every event. Were it ever read as
// ask-aaab's, a harvest would copy it and a resolve would fire it.
func substraypinPlant(t *testing.T, s *FileStore, name string) string {
	t.Helper()
	dir := filepath.Join(s.Root(), subsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"1","item":"ask-aaab","subscriptions":[{"id":"deadbeef",` +
		`"events":["resolve","close","reopen"],"handler":"planted","args":{},"added_at":"2026-05-15T11:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestSubsStrayHarvestSourceRefusesBeforeAnyWrite: harvesting FROM a
// store with a subs stray is refused before anything is written. Nothing
// reaches the target (not the item with the stray, not the bystander
// beside it), nothing is cleaned from the source, and the error names the
// stray file and the item.
func TestSubsStrayHarvestSourceRefusesBeforeAnyWrite(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for _, stray := range []string{"ASK-AAAB.json", "ask-aaab.JSON", "Ask-aaab.json"} {
		tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A")
		src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
			NewItem("ask-aaab", "has a stray", UrgencyNormal, StatusOpen, base),
			NewItem("ask-cccc", "bystander", UrgencyNormal, StatusOpen, base))
		substraypinPlant(t, src, stray)
		tgtBefore, srcBefore := substrayTree(t, tgt), substrayTree(t, src)

		for _, clean := range []bool{false, true} {
			res, err := Harvest(tgt, src, clean)
			if err == nil {
				t.Fatalf("%s clean=%v: harvest went through: %+v", stray, clean, res)
			}
			if !errors.Is(err, ErrUnreadableSource) || !errors.Is(err, ErrStraySubs) {
				t.Errorf("%s clean=%v: want ErrUnreadableSource wrapping ErrStraySubs, got %v", stray, clean, err)
			}
			if !strings.Contains(err.Error(), "subs/"+stray) || !strings.Contains(err.Error(), "ask-aaab") {
				t.Errorf("%s clean=%v: error must name subs/%s and ask-aaab, got %q", stray, clean, stray, err)
			}
			if res != nil {
				t.Errorf("%s clean=%v: refused harvest returned a result: %+v", stray, clean, res)
			}
			substraySame(t, stray+" target", tgtBefore, substrayTree(t, tgt))
			substraySame(t, stray+" source", srcBefore, substrayTree(t, src))
			if ids, err := tgt.ListIDs(); err != nil || len(ids) != 0 {
				t.Errorf("%s clean=%v: target holds items %v (%v), want none", stray, clean, ids, err)
			}
			if entries, err := os.ReadDir(filepath.Join(tgt.Root(), subsSubdir)); err == nil && len(entries) != 0 {
				t.Errorf("%s clean=%v: target subs dir holds %d entries, want none", stray, clean, len(entries))
			}
		}
	}
}
