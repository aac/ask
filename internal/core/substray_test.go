package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-48158e: a file in the TARGET's .ask/subs/ whose name
// differs from a source item's subs file only in case (ASK-AAAB.json
// against ask-aaab.json). Reproduced on macOS before the fix: harvest
// exited 0, overwrote the hand-placed file's contents and left it named
// ASK-AAAB.json, so the harvested subscriptions sat under a name no
// case-sensitive filesystem would find. Harvest refuses by comparing
// names, so the same store is refused on a case-sensitive filesystem too.

// substrayTree returns relative path -> contents for every file under a
// store's .ask directory, lock file excepted.
func substrayTree(t *testing.T, s *FileStore) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(s.Root(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".lock" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.Root(), p)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func substraySame(t *testing.T, what string, before, after map[string]string) {
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

// substrayPlant hand-places a file in a store's subs directory.
func substrayPlant(t *testing.T, s *FileStore, name string) {
	t.Helper()
	dir := filepath.Join(s.Root(), subsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("hand-placed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func substraySubscribe(t *testing.T, s *FileStore, id string) {
	t.Helper()
	now := time.Date(2026, 5, 15, 11, 0, 0, 0, time.UTC)
	if _, err := s.Subscribe(id, []string{EventResolve}, "h", nil, now); err != nil {
		t.Fatal(err)
	}
}

func TestSubstrayHarvestRefusesCaseTwinInTargetSubs(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for _, stray := range []string{"ASK-AAAB.json", "ask-aaab.JSON", "Ask-aaab.json"} {
		tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A")
		substrayPlant(t, tgt, stray)
		src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
			NewItem("ask-aaab", "subscribed", UrgencyNormal, StatusOpen, base),
			NewItem("ask-cccc", "bystander", UrgencyNormal, StatusOpen, base))
		substraySubscribe(t, src, "ask-aaab")
		tgtBefore, srcBefore := substrayTree(t, tgt), substrayTree(t, src)

		for _, clean := range []bool{false, true} {
			res, err := Harvest(tgt, src, clean)
			if !errors.Is(err, ErrStrayTarget) {
				t.Fatalf("%s clean=%v: want ErrStrayTarget, got %+v, %v", stray, clean, res, err)
			}
			if !strings.Contains(err.Error(), "subs/"+stray) || !strings.Contains(err.Error(), "ask-aaab") {
				t.Errorf("%s clean=%v: error must name the stray subs file and the id, got %q", stray, clean, err)
			}
			substraySame(t, stray+" target", tgtBefore, substrayTree(t, tgt))
			substraySame(t, stray+" source", srcBefore, substrayTree(t, src))
		}
	}
}

// Only a subs file harvest would write is in the way: a stray matching a
// source item that has no subscriptions is left alone, and the harvest
// goes through. A subs twin is reported together with an items twin and a
// collision.
func TestSubstrayHarvestOnlyWrittenSubsRefuse(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
		NewItem("ask-aaaa", "target's", UrgencyNormal, StatusOpen, base))
	substrayPlant(t, tgt, "ASK-CCCC.json")
	src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
		NewItem("ask-cccc", "no subscriptions", UrgencyNormal, StatusOpen, base))
	if res, err := Harvest(tgt, src, false); err != nil || len(res.Harvested) != 1 {
		t.Fatalf("a stray no subs file would be written over must not refuse: %+v, %v", res, err)
	}
	if b, err := os.ReadFile(filepath.Join(tgt.Root(), subsSubdir, "ASK-CCCC.json")); err != nil || string(b) != "hand-placed\n" {
		t.Errorf("stray was touched: %q, %v", b, err)
	}

	strayPlant(t, tgt, "ASK-DDDD")
	substrayPlant(t, tgt, "ASK-EEEE.json")
	src2 := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000C",
		NewItem("ask-aaaa", "collides", UrgencyNormal, StatusOpen, base),
		NewItem("ask-dddd", "items twin", UrgencyNormal, StatusOpen, base),
		NewItem("ask-eeee", "subs twin", UrgencyNormal, StatusOpen, base))
	substraySubscribe(t, src2, "ask-eeee")
	tgtBefore, srcBefore := substrayTree(t, tgt), substrayTree(t, src2)
	_, err := Harvest(tgt, src2, true)
	if !errors.Is(err, ErrStrayTarget) || !errors.Is(err, ErrIDCollision) {
		t.Fatalf("want ErrStrayTarget and ErrIDCollision together, got %v", err)
	}
	for _, want := range []string{"ASK-DDDD.json (source ask-dddd)", "subs/ASK-EEEE.json (source ask-eeee)", "ask-aaaa"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got %q", want, err)
		}
	}
	substraySame(t, "target", tgtBefore, substrayTree(t, tgt))
	substraySame(t, "source", srcBefore, substrayTree(t, src2))
}
