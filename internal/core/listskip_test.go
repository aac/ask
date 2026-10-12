package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// listskipCorrupt is a file that is not JSON at all; listskipV2 is a
// well-formed item a newer ask is imagined to have written.
const listskipCorrupt = `{"id": "ask-`

func listskipV2(id string) []byte {
	return schemaverItemJSON(id, `"schema_version":"2",`)
}

// listskipSnapshot returns every item file's bytes, keyed by file name, so
// a test can assert List left the store byte-identical.
func listskipSnapshot(t *testing.T, s *FileStore) map[string]string {
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

func listskipIDs(skipped []SkippedItem) []string {
	ids := make([]string, len(skipped))
	for i, sk := range skipped {
		ids[i] = sk.ID
	}
	return ids
}

// TestListskipMixedStore: one readable item, one corrupt file, one
// schema_version "2" file. List returns the readable item and reports the
// other two as skipped, sorted by id, each with the error Load gives for
// it — and rewrites nothing (spec §7.2).
func TestListskipMixedStore(t *testing.T) {
	s := tempStore(t)
	if err := s.Save(NewItem("ask-0001", "ok", UrgencyNormal, StatusOpen, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	schemaverWriteRaw(t, s, "ask-0003", listskipV2("ask-0003"))
	schemaverWriteRaw(t, s, "ask-0002", []byte(listskipCorrupt))
	before := listskipSnapshot(t, s)

	items, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ask-0001" {
		t.Fatalf("items: want [ask-0001], got %d", len(items))
	}
	if got := listskipIDs(skipped); len(got) != 2 || got[0] != "ask-0002" || got[1] != "ask-0003" {
		t.Fatalf("skipped: want [ask-0002 ask-0003], got %v", got)
	}
	if skipped[0].Err == nil || errors.Is(skipped[0].Err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("corrupt file: want a plain decode error, got %v", skipped[0].Err)
	}
	if !errors.Is(skipped[1].Err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("v2 file: want ErrUnsupportedSchemaVersion, got %v", skipped[1].Err)
	}
	// The skip entry is exactly what addressing the item directly returns.
	for _, sk := range skipped {
		_, loadErr := s.Load(sk.ID)
		if loadErr == nil || loadErr.Error() != sk.Err.Error() {
			t.Fatalf("%s: skip error %q differs from Load error %v", sk.ID, sk.Err, loadErr)
		}
	}
	after := listskipSnapshot(t, s)
	if len(after) != len(before) {
		t.Fatalf("List changed the file set: %d -> %d", len(before), len(after))
	}
	for name, b := range before {
		if after[name] != b {
			t.Fatalf("List rewrote %s", name)
		}
	}
}

// TestListskipAllUnreadable: List itself still returns no error — it
// reports zero items and every id as skipped, and the caller applies the
// §7.2 "every item unreadable" rule.
func TestListskipAllUnreadable(t *testing.T) {
	s := tempStore(t)
	schemaverWriteRaw(t, s, "ask-0001", []byte(listskipCorrupt))
	schemaverWriteRaw(t, s, "ask-0002", listskipV2("ask-0002"))

	items, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items: want none, got %d", len(items))
	}
	if got := listskipIDs(skipped); len(got) != 2 || got[0] != "ask-0001" || got[1] != "ask-0002" {
		t.Fatalf("skipped: want [ask-0001 ask-0002], got %v", got)
	}
}

// TestListskipEmptyStore: no items and nothing skipped, both before the
// items directory exists and after it has been created empty — so a caller
// can tell an empty inbox from an inbox of unreadable files.
func TestListskipEmptyStore(t *testing.T) {
	s := tempStore(t)
	for _, step := range []string{"no items dir", "empty items dir"} {
		if step == "empty items dir" {
			if err := os.MkdirAll(filepath.Join(s.Root(), itemsSubdir), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		items, skipped, err := s.List()
		if err != nil || len(items) != 0 || len(skipped) != 0 {
			t.Fatalf("%s: want empty/empty/nil, got items=%d skipped=%v err=%v", step, len(items), skipped, err)
		}
	}
}
