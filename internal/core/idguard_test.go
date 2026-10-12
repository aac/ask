package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin act-7ee74d: the id inside an item file is not trusted.
// It must equal the filename stem, and no id that is not a plain file
// name may be turned into a path. Every fixture lives under t.TempDir().

// idguardStore opens a fresh store at <parent>/<name>, so that "two levels
// above items/" (the project directory) is still inside the test's temp
// dir.
func idguardStore(t *testing.T, parent, name string) (*FileStore, string) {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir, &ProjectConfig{
		ProjectID:   "01HXIDGUARD0000000000000" + strings.ToUpper(name[:1]) + "A",
		DisplayName: name,
		CreatedAt:   time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("OpenStore %s: %v", dir, err)
	}
	return s, dir
}

// idguardPlant writes an item file named <fileID>.json whose "id" member
// is contentID — the file ask itself would write, with only the id
// rewritten. Returns the file's path and bytes.
func idguardPlant(t *testing.T, s *FileStore, fileID, contentID string) (string, []byte) {
	t.Helper()
	it := NewItem(fileID, "planted "+fileID, UrgencyNormal, StatusOpen,
		time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	if err := s.Save(it); err != nil {
		t.Fatalf("Save %s: %v", fileID, err)
	}
	p := filepath.Join(s.Root(), itemsSubdir, fileID+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	from := []byte(`"id": "` + fileID + `"`)
	if !bytes.Contains(b, from) {
		t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
	}
	out := bytes.Replace(b, from, []byte(`"id": "`+contentID+`"`), 1)
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, out
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
func idguardSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for name, b := range before {
		a, ok := after[name]
		if !ok {
			t.Errorf("%s: %s was removed", what, name)
		} else if a != b {
			t.Errorf("%s: %s was rewritten:\n%s", what, name, a)
		}
	}
	for name := range after {
		// The store's advisory lock file is created by any locked write
		// path; it holds no item data and is not what these tests guard.
		if filepath.Base(name) == ".lock" {
			continue
		}
		if _, ok := before[name]; !ok {
			t.Errorf("%s: %s was created", what, name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

// TestIdguardLoadRefusesMismatchedID: a file whose "id" differs from its
// filename stem is unreadable, and the error names both ids.
func TestIdguardLoadRefusesMismatchedID(t *testing.T) {
	for _, content := range []string{"../../victim", "ask-bbbb", ""} {
		s, _ := idguardStore(t, t.TempDir(), "src")
		idguardPlant(t, s, "ask-aaaa", content)
		it, err := s.Load("ask-aaaa")
		if err == nil {
			t.Fatalf("content id %q: Load succeeded with item id %q", content, it.ID)
		}
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("content id %q: want ErrIDMismatch, got %v", content, err)
		}
		if !strings.Contains(err.Error(), "ask-aaaa") || !strings.Contains(err.Error(), `"`+content+`"`) {
			t.Fatalf("content id %q: error must name both ids, got %q", content, err)
		}
	}
}

// TestIdguardListSkipsMismatchedID: `list` treats the mismatch like any
// other unreadable file — skipped under its filename id, siblings kept.
func TestIdguardListSkipsMismatchedID(t *testing.T) {
	s, _ := idguardStore(t, t.TempDir(), "src")
	idguardPlant(t, s, "ask-aaaa", "../../victim")
	idguardPlant(t, s, "ask-cccc", "ask-cccc")
	items, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ask-cccc" {
		t.Fatalf("items = %+v, want just ask-cccc", items)
	}
	if len(skipped) != 1 || skipped[0].ID != "ask-aaaa" || !errors.Is(skipped[0].Err, ErrIDMismatch) {
		t.Fatalf("skipped = %+v, want ask-aaaa with ErrIDMismatch", skipped)
	}
}

// TestIdguardStoreRefusesNonPlainIDs: Save and Load never turn an id that
// is not a plain file name into a path. The victim file sits exactly where
// the traversing id points, so a Load that followed it would succeed and a
// Save would rewrite it.
func TestIdguardStoreRefusesNonPlainIDs(t *testing.T) {
	s, dir := idguardStore(t, t.TempDir(), "src")
	victim := filepath.Join(dir, "victim.json")
	valid, err := json.Marshal(NewItem("../../victim", "v", UrgencyNormal, StatusOpen, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, valid, 0o644); err != nil {
		t.Fatal(err)
	}
	before := idguardTree(t, dir)
	for _, id := range []string{
		"", ".", "..", "../../victim", "a/b", `a\b`, `..\..\victim`, "ask-..", "ask-aa\x00aa",
	} {
		if _, err := s.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q): want ErrInvalidID, got %v", id, err)
		}
		it := NewItem(id, "t", UrgencyNormal, StatusOpen, time.Now().UTC())
		if err := s.Save(it); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Save(%q): want ErrInvalidID, got %v", id, err)
		}
	}
	idguardSameTree(t, "store dir", before, idguardTree(t, dir))
}

// TestIdguardHarvestTraversalIDTouchesNothing is the acceptance scenario
// at the core layer: source ask-aaaa.json carries "id": "../../victim",
// and a victim.json sits two levels above items/ in both stores. Harvest
// with clean must fail and leave both trees byte-identical.
func TestIdguardHarvestTraversalIDTouchesNothing(t *testing.T) {
	tmp := t.TempDir()
	source, srcDir := idguardStore(t, tmp, "src")
	target, tgtDir := idguardStore(t, tmp, "tgt")
	idguardPlant(t, source, "ask-aaaa", "../../victim")
	for _, d := range []string{srcDir, tgtDir} {
		if err := os.WriteFile(filepath.Join(d, "victim.json"), []byte("victim of "+d+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := idguardTree(t, tmp)

	res, err := Harvest(target, source, true)
	if err == nil {
		t.Errorf("Harvest succeeded: %+v", res)
	} else {
		if !errors.Is(err, ErrUnreadableSource) || !errors.Is(err, ErrIDMismatch) {
			t.Errorf("want ErrUnreadableSource wrapping ErrIDMismatch, got %v", err)
		}
		if !strings.Contains(err.Error(), "ask-aaaa") || !strings.Contains(err.Error(), "../../victim") {
			t.Errorf("error must name both ids, got %q", err)
		}
	}
	idguardSameTree(t, "stores", before, idguardTree(t, tmp))
}

// TestIdguardHarvestMismatchCannotOverwriteTargetItem: the content id is a
// different, valid-looking id that already exists in the target. The
// collision preflight compares filenames (ask-aaaa is free), so only the
// mismatch check stands between this file and the target's ask-bbbb.
func TestIdguardHarvestMismatchCannotOverwriteTargetItem(t *testing.T) {
	tmp := t.TempDir()
	source, _ := idguardStore(t, tmp, "src")
	target, _ := idguardStore(t, tmp, "tgt")
	idguardPlant(t, source, "ask-aaaa", "ask-bbbb")
	idguardPlant(t, target, "ask-bbbb", "ask-bbbb")
	before := idguardTree(t, tmp)

	for _, clean := range []bool{false, true} {
		res, err := Harvest(target, source, clean)
		if err == nil {
			t.Errorf("clean=%v: Harvest succeeded: %+v", clean, res)
		} else if !errors.Is(err, ErrIDMismatch) {
			t.Errorf("clean=%v: want ErrIDMismatch, got %v", clean, err)
		}
		idguardSameTree(t, "stores", before, idguardTree(t, tmp))
	}
}
