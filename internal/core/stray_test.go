package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-16ad85: a file in .ask/items/ whose name is not a
// valid id (ASK-AAAB.json) is a stray. It never stands between a prefix
// and a real item, and when it is the only thing a prefix matches the
// error names the file. Harvest reports an id collision and an unreadable
// source item in one run.

// strayPlant writes a well-formed item file under a name that is not a
// valid id. ASK-AAAB.json beside ask-aaaa.json is safe on a
// case-insensitive filesystem; ASK-AAAA.json beside ask-aaaa.json is not,
// so that pairing is only ever exercised through ResolvePrefix's pool.
func strayPlant(t *testing.T, s *FileStore, name string) {
	t.Helper()
	b := []byte(`{"schema_version":"1","id":"` + name + `","title":"stray","status":"open","urgency":"normal","created_at":"2026-05-15T10:00:00Z"}`)
	dir := filepath.Join(s.Root(), itemsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStrayResolvePrefix(t *testing.T) {
	// The first pool is what a case-sensitive filesystem can hold: a real
	// item and a stray whose name differs from it only in case.
	both := []string{"ASK-AAAA", "ASK-AAAB", "ask-aaaa"}
	for _, tc := range []struct {
		name, input string
		pool        []string
		want        string
	}{
		{"full id beside its case twin", "ask-aaaa", both, "ask-aaaa"},
		{"bare hex beside its case twin", "aaaa", both, "ask-aaaa"},
		{"upper-case input still means the real item", "ASK-AAAA", both, "ask-aaaa"},
		{"prefix shared with strays", "aaa", both, "ask-aaaa"},
	} {
		got, err := ResolvePrefix(tc.input, tc.pool)
		if err != nil || got != tc.want {
			t.Errorf("%s: ResolvePrefix(%q) = %q, %v; want %q", tc.name, tc.input, got, err, tc.want)
		}
	}

	// Only a stray matches: not "not found" (the file is there), and the
	// message names the file.
	_, err := ResolvePrefix("aaab", both)
	if !errors.Is(err, ErrInvalidID) || errors.Is(err, ErrIDNotFound) || errors.Is(err, ErrIDAmbiguous) {
		t.Fatalf("ResolvePrefix(aaab): want ErrInvalidID only, got %v", err)
	}
	if !strings.Contains(err.Error(), "ASK-AAAB.json") {
		t.Errorf("ResolvePrefix(aaab): error must name the stray file, got %q", err)
	}

	// Ambiguity is among real items only.
	_, err = ResolvePrefix("aaa", []string{"ASK-AAAB", "ask-aaaa", "ask-aaac"})
	if !errors.Is(err, ErrIDAmbiguous) {
		t.Fatalf("ResolvePrefix(aaa): want ErrIDAmbiguous, got %v", err)
	}
	if strings.Contains(err.Error(), "ASK-AAAB") {
		t.Errorf("ambiguity must not list the stray, got %q", err)
	}

	if _, err := ResolvePrefix("b", both); !errors.Is(err, ErrIDNotFound) {
		t.Errorf("ResolvePrefix(b): want ErrIDNotFound, got %v", err)
	}
}

// TestStrayHarvestReportsCollisionAndUnreadable: the source holds
// ask-aaaa (which the target also holds) and a stray ASK-AAAB.json. One
// run names both problems and touches neither store.
func TestStrayHarvestReportsCollisionAndUnreadable(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
		NewItem("ask-aaaa", "target's", UrgencyNormal, StatusOpen, base))
	src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
		NewItem("ask-aaaa", "source's", UrgencyNormal, StatusOpen, base))
	strayPlant(t, src, "ASK-AAAB")

	for _, clean := range []bool{false, true} {
		_, err := Harvest(tgt, src, clean)
		if !errors.Is(err, ErrIDCollision) || !errors.Is(err, ErrUnreadableSource) || !errors.Is(err, ErrInvalidID) {
			t.Fatalf("clean=%v: want ErrIDCollision, ErrUnreadableSource and ErrInvalidID together, got %v", clean, err)
		}
		if !strings.Contains(err.Error(), "ask-aaaa") || !strings.Contains(err.Error(), "ASK-AAAB") {
			t.Errorf("clean=%v: error must name both ids, got %q", clean, err)
		}
	}
	if it, err := tgt.Load("ask-aaaa"); err != nil || it.Title != "target's" {
		t.Errorf("target item changed: %+v, %v", it, err)
	}
	if ids, _ := tgt.ListIDs(); len(ids) != 1 {
		t.Errorf("target gained files: %v", ids)
	}
	if ids, _ := src.ListIDs(); len(ids) != 2 {
		t.Errorf("source lost files: %v", ids)
	}
}

// A collision alone, and an unreadable source alone, each still report
// only themselves.
func TestStrayHarvestSingleProblemUnchanged(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	tgt := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
		NewItem("ask-aaaa", "target's", UrgencyNormal, StatusOpen, base))
	src := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000B",
		NewItem("ask-aaaa", "source's", UrgencyNormal, StatusOpen, base))
	_, err := Harvest(tgt, src, false)
	if !errors.Is(err, ErrIDCollision) || errors.Is(err, ErrUnreadableSource) {
		t.Errorf("collision only: got %v", err)
	}

	src2 := harvestSeed(t, t.TempDir(), "01HXWORK0000000000000000C")
	strayPlant(t, src2, "ASK-AAAB")
	_, err = Harvest(tgt, src2, false)
	if errors.Is(err, ErrIDCollision) || !errors.Is(err, ErrUnreadableSource) {
		t.Errorf("unreadable only: got %v", err)
	}
}
