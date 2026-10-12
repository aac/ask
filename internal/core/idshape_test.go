package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The store accepts exactly the id shape NewID generates (spec §1.1,
// `^ask-[0-9a-f]{4}$`) and nothing else (act-1e046d).

var idshapeBase = time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)

func idshapeItem(id, title string) *Item {
	return &Item{ID: id, Title: title, Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: idshapeBase, Links: []Link{}}
}

// idshapeWriteRaw puts an item file on disk under an arbitrary file-name
// id, bypassing Save — the way a hand edit or a foreign tool would.
func idshapeWriteRaw(t *testing.T, s *FileStore, id, title string) string {
	t.Helper()
	dir := filepath.Join(s.Root(), itemsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"1","id":"` + id + `","title":"` + title +
		`","urgency":"normal","status":"open","created_at":"2026-05-15T10:00:00Z","links":[]}`
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// idshapeCaseInsensitive reports whether dir's filesystem treats names
// differing only in case as one file. Detected, never assumed.
func idshapeCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "idshape-probe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "IDSHAPE-PROBE"))
	_ = os.Remove(filepath.Join(dir, "idshape-probe"))
	return err == nil
}

var idshapeBadIDs = []string{"ASK-AAAA", "ask-AAAA", "Ask-aaaa", "ask-xyz1", "ask-aaaaa", "ask-aaa", "ask-", "aaaa", "bsk-aaaa", "ask-aaa ", "ask-aa.a"}

func TestIDShapeSaveAndLoadRefuseNonConformingIDs(t *testing.T) {
	s := tempStore(t)
	for _, id := range idshapeBadIDs {
		if err := s.Save(idshapeItem(id, "x")); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Save(%q): want ErrInvalidID, got %v", id, err)
		}
		if _, err := s.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q): want ErrInvalidID, got %v", id, err)
		}
	}
	if ids, _ := s.ListIDs(); len(ids) != 0 {
		t.Fatalf("a refused Save wrote something: %v", ids)
	}
}

func TestIDShapeValidIDRoundTrips(t *testing.T) {
	s := tempStore(t)
	for _, id := range []string{"ask-aaaa", "ask-0000", "ask-09af", "ask-3c89"} {
		if err := s.Save(idshapeItem(id, "t-"+id)); err != nil {
			t.Fatalf("Save(%q): %v", id, err)
		}
		got, err := s.Load(id)
		if err != nil {
			t.Fatalf("Load(%q): %v", id, err)
		}
		if got.ID != id || got.Title != "t-"+id {
			t.Fatalf("round trip %q: got id=%q title=%q", id, got.ID, got.Title)
		}
	}
}

// A generated id always passes the store's own check: the validator is
// derived from the same constants as the generator.
func TestIDShapeGeneratedIDsAreAccepted(t *testing.T) {
	for i := 0; i < 200; i++ {
		id, err := NewID("p", idshapeBase.Add(time.Duration(i)), "t", func(string) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		if err := checkID(id); err != nil {
			t.Fatalf("generated id refused: %v", err)
		}
	}
}

// A stray non-conforming .json file in items/ is an unreadable item: List
// skips it with a per-id reason and still returns the readable ones.
func TestIDShapeListSkipsStrayNonConformingFile(t *testing.T) {
	s := tempStore(t)
	if err := s.Save(idshapeItem("ask-bbbb", "good")); err != nil {
		t.Fatal(err)
	}
	idshapeWriteRaw(t, s, "ask-xyz1", "stray")
	idshapeWriteRaw(t, s, "notes", "stray2")

	items, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ask-bbbb" {
		t.Fatalf("want only ask-bbbb listed, got %+v", items)
	}
	if len(skipped) != 2 {
		t.Fatalf("want 2 skipped, got %+v", skipped)
	}
	for _, sk := range skipped {
		if !errors.Is(sk.Err, ErrInvalidID) {
			t.Errorf("skip %s: want ErrInvalidID, got %v", sk.ID, sk.Err)
		}
		if !strings.Contains(sk.String(), "skipped "+sk.ID) {
			t.Errorf("skip warning does not name the id: %q", sk.String())
		}
	}
}

// Harvest refuses the whole run on a non-conforming source file name —
// never skips it — naming every bad id, with both stores untouched.
func TestIDShapeHarvestRefusesNonConformingSourceIDs(t *testing.T) {
	source := harvestSeed(t, t.TempDir(), "01HXSOURCE000000000000000A", idshapeItem("ask-cccc", "ok"))
	target := harvestSeed(t, t.TempDir(), "01HXTARGET000000000000000B")
	p1 := idshapeWriteRaw(t, source, "ask-xyz1", "bad1")
	p2 := idshapeWriteRaw(t, source, "ask-ddddd", "bad2")

	_, err := Harvest(target, source, true)
	if !errors.Is(err, ErrUnreadableSource) || !errors.Is(err, ErrInvalidID) {
		t.Fatalf("want ErrUnreadableSource wrapping ErrInvalidID, got %v", err)
	}
	for _, id := range []string{"ask-xyz1", "ask-ddddd"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("error does not name %s: %v", id, err)
		}
	}
	if ids, _ := target.ListIDs(); len(ids) != 0 {
		t.Fatalf("target written despite refusal: %v", ids)
	}
	for _, p := range []string{p1, p2, filepath.Join(source.Root(), itemsSubdir, "ask-cccc.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("source file gone after refused --clean harvest: %v", err)
		}
	}
}

// The reported bug: on a case-insensitive filesystem a source ASK-AAAA.json
// passed the exact-string collision preflight and Save then overwrote the
// target's ask-aaaa.json.
func TestIDShapeHarvestCaseCollisionDoesNotOverwriteTarget(t *testing.T) {
	srcDir, tgtDir := t.TempDir(), t.TempDir()
	if !idshapeCaseInsensitive(t, tgtDir) {
		t.Skip("temp dir filesystem is case-sensitive; ASK-AAAA.json and ask-aaaa.json are distinct files here")
	}
	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A")
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B", idshapeItem("ask-aaaa", "target original"))
	idshapeWriteRaw(t, source, "ASK-AAAA", "source imposter")

	tgtPath := filepath.Join(target.Root(), itemsSubdir, "ask-aaaa.json")
	before, err := os.ReadFile(tgtPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Harvest(target, source, false)
	if !errors.Is(err, ErrUnreadableSource) || !errors.Is(err, ErrInvalidID) {
		t.Fatalf("want ErrUnreadableSource wrapping ErrInvalidID, got %v", err)
	}
	if !strings.Contains(err.Error(), "ASK-AAAA") {
		t.Errorf("error does not name ASK-AAAA: %v", err)
	}
	after, err := os.ReadFile(tgtPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("target ask-aaaa.json was overwritten:\nbefore: %s\nafter:  %s", before, after)
	}
}
