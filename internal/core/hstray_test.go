package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-f9ab23: a file in the TARGET's .ask/items/ whose
// name differs from a source item's file only in case (ASK-AAAB.json
// against ask-aaab.json). On a case-insensitive filesystem the copy would
// land in that file and keep its name, leaving the harvested ask
// unreadable. Harvest refuses by comparing names, so the same store is
// refused on a case-sensitive filesystem too.

// hstrayFiles returns name -> contents for a store's items directory.
func hstrayFiles(t *testing.T, s *FileStore) map[string]string {
	t.Helper()
	dir := filepath.Join(s.Root(), itemsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func hstraySame(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: files changed: before %d, after %d", what, len(before), len(after))
	}
	for name, b := range before {
		if a, ok := after[name]; !ok || a != b {
			t.Errorf("%s: %s changed or missing", what, name)
		}
	}
}

func TestHstrayHarvestRefusesCaseTwinInTarget(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for _, stray := range []string{"ASK-AAAB.json", "ask-aaab.JSON", "Ask-aaab.json"} {
		tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A")
		dir := filepath.Join(tgt.Root(), itemsSubdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, stray), []byte("hand-placed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
			NewItem("ask-aaab", "source's", UrgencyNormal, StatusOpen, base),
			NewItem("ask-cccc", "bystander", UrgencyNormal, StatusOpen, base))
		tgtBefore, srcBefore := hstrayFiles(t, tgt), hstrayFiles(t, src)

		for _, clean := range []bool{false, true} {
			res, err := Harvest(tgt, src, clean)
			if !errors.Is(err, ErrStrayTarget) {
				t.Fatalf("%s clean=%v: want ErrStrayTarget, got %+v, %v", stray, clean, res, err)
			}
			if !strings.Contains(err.Error(), stray) || !strings.Contains(err.Error(), "ask-aaab") {
				t.Errorf("%s clean=%v: error must name the stray file and the id, got %q", stray, clean, err)
			}
			hstraySame(t, stray+" target", tgtBefore, hstrayFiles(t, tgt))
			hstraySame(t, stray+" source", srcBefore, hstrayFiles(t, src))
		}
	}
}

// A stray in the target that is no source item's twin does not stand in
// the way, and a twin is reported together with a collision.
func TestHstrayHarvestOnlyTwinsRefuse(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
		NewItem("ask-aaaa", "target's", UrgencyNormal, StatusOpen, base))
	strayPlant(t, tgt, "ASK-AAAB")
	src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
		NewItem("ask-cccc", "unrelated", UrgencyNormal, StatusOpen, base))
	if res, err := Harvest(tgt, src, false); err != nil || len(res.Harvested) != 1 {
		t.Fatalf("unrelated stray must not refuse: %+v, %v", res, err)
	}

	src2 := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000C",
		NewItem("ask-aaaa", "collides", UrgencyNormal, StatusOpen, base),
		NewItem("ask-aaab", "twin", UrgencyNormal, StatusOpen, base))
	_, err := Harvest(tgt, src2, true)
	if !errors.Is(err, ErrStrayTarget) || !errors.Is(err, ErrIDCollision) {
		t.Fatalf("want ErrStrayTarget and ErrIDCollision together, got %v", err)
	}
	if !strings.Contains(err.Error(), "ASK-AAAB.json") || !strings.Contains(err.Error(), "ask-aaaa") {
		t.Errorf("error must name the stray file and the colliding id, got %q", err)
	}
	if ids, _ := src2.ListIDs(); len(ids) != 2 {
		t.Errorf("source lost files: %v", ids)
	}
}
