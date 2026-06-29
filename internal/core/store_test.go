package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir, &ProjectConfig{ProjectID: "p1", DisplayName: "test", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreSaveAndLoad(t *testing.T) {
	s := tempStore(t)
	item := &Item{
		ID:        "ask-3c89",
		Title:     "x",
		Urgency:   UrgencyNormal,
		Status:    StatusOpen,
		CreatedAt: time.Now(),
	}
	if err := s.Save(item); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("ask-3c89")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "x" {
		t.Fatalf("title mismatch: %q", got.Title)
	}
}

func TestStoreListReturnsIDs(t *testing.T) {
	s := tempStore(t)
	for _, id := range []string{"ask-3c89", "ask-7ecd"} {
		if err := s.Save(&Item{ID: id, Title: id, Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.ListIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
}

func TestStoreAtomicRename(t *testing.T) {
	s := tempStore(t)
	item := &Item{ID: "ask-aaaa", Title: "x", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: time.Now()}
	if err := s.Save(item); err != nil {
		t.Fatal(err)
	}
	// No leftover .tmp file.
	entries, _ := os.ReadDir(filepath.Join(s.Root(), "items"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("found .tmp leftover: %s", e.Name())
		}
	}
}

// TestStoreLoadMissingReturnsSentinel ensures Load returns the package-level
// ErrIDNotFound sentinel so the CLI can map it to exit code 3 (spec §4.3).
func TestStoreLoadMissingReturnsSentinel(t *testing.T) {
	s := tempStore(t)
	_, err := s.Load("ask-nope")
	if err == nil {
		t.Fatal("expected error for missing id, got nil")
	}
	if err != ErrIDNotFound {
		t.Fatalf("expected ErrIDNotFound, got %v", err)
	}
}

// TestStoreListSorted verifies List() returns items in the spec §3.2 default
// order: urgency desc (blocker, normal, fyi), then created_at ascending,
// then id ascending as a stable tiebreaker.
func TestStoreListSorted(t *testing.T) {
	s := tempStore(t)
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	items := []*Item{
		{ID: "ask-0001", Title: "fyi-old", Urgency: UrgencyFYI, Status: StatusOpen, CreatedAt: base},
		{ID: "ask-0002", Title: "blocker-new", Urgency: UrgencyBlocker, Status: StatusOpen, CreatedAt: base.Add(2 * time.Hour)},
		{ID: "ask-0003", Title: "blocker-old", Urgency: UrgencyBlocker, Status: StatusOpen, CreatedAt: base.Add(1 * time.Hour)},
		{ID: "ask-0004", Title: "normal-mid", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base.Add(90 * time.Minute)},
		// Same urgency and timestamp as 0004 -> id tiebreaker (0004 < 0005).
		{ID: "ask-0005", Title: "normal-mid-tie", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base.Add(90 * time.Minute)},
	}
	for _, it := range items {
		if err := s.Save(it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ask-0003", "ask-0002", "ask-0004", "ask-0005", "ask-0001"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d: got %s want %s", i, got[i].ID, id)
		}
	}
}

// TestOpenStoreLoadsExistingConfig verifies the second-open path preserves
// the on-disk config (idempotency per spec §6 case 2) and ignores any cfg
// the caller passes in.
func TestOpenStoreLoadsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenStore(dir, &ProjectConfig{ProjectID: "p-original", DisplayName: "first", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if first.Config().ProjectID != "p-original" {
		t.Fatalf("first open: ProjectID=%q", first.Config().ProjectID)
	}
	second, err := OpenStore(dir, &ProjectConfig{ProjectID: "p-other", DisplayName: "other", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if second.Config().ProjectID != "p-original" {
		t.Fatalf("second open should preserve original project_id, got %q", second.Config().ProjectID)
	}
	if second.Config().DisplayName != "first" {
		t.Fatalf("second open should preserve display_name, got %q", second.Config().DisplayName)
	}
}

// TestOpenStoreCorruptConfig verifies a corrupt config.json surfaces an
// error (mapped to exit code 5 in the CLI per spec §7).
func TestOpenStoreCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ask", "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ask", "config.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(dir, nil)
	if err == nil {
		t.Fatal("expected error opening store with corrupt config, got nil")
	}
}

// TestStoreSaveStampsSchemaVersionOnDisk verifies the write path always
// emits schema_version on disk, even when the caller constructs an Item
// via struct literal and never sets the field. This is the
// belt-and-suspenders guarantee on top of NewItem (spec §1.1, §12;
// distribution-readiness §6).
func TestStoreSaveStampsSchemaVersionOnDisk(t *testing.T) {
	s := tempStore(t)
	item := &Item{
		ID:        "ask-aaaa",
		Title:     "x",
		Urgency:   UrgencyNormal,
		Status:    StatusOpen,
		CreatedAt: time.Now(),
		// SchemaVersion deliberately left zero.
	}
	if err := s.Save(item); err != nil {
		t.Fatal(err)
	}
	if item.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("Save should stamp in-memory schema_version, got %q", item.SchemaVersion)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root(), "items", "ask-aaaa.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"schema_version": "1"`) {
		t.Fatalf("expected schema_version: \"1\" in on-disk JSON, got: %s", string(raw))
	}
}

// TestStoreLoadTolerantOfMissingSchemaVersion writes a fixture item file
// directly (as a pre-schema_version version of ask would have) and
// confirms Load returns it with SchemaVersion silently defaulted to "1".
// This pins the load-bearing backward-compat read path for stores that
// existed before the field was added.
func TestStoreLoadTolerantOfMissingSchemaVersion(t *testing.T) {
	s := tempStore(t)
	legacy := []byte(`{
  "id": "ask-1234",
  "title": "legacy item",
  "body": "",
  "urgency": "normal",
  "status": "open",
  "filed_by": null,
  "tracker_ref": null,
  "verifier": null,
  "links": [],
  "blocks": [],
  "resolution_note": null,
  "verification_output": null,
  "created_at": "2026-05-15T10:30:00Z",
  "resolved_at": null,
  "verified_at": null,
  "closed_at": null
}`)
	if err := os.WriteFile(filepath.Join(s.Root(), "items", "ask-1234.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("ask-1234")
	if err != nil {
		t.Fatalf("load legacy item: %v", err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("legacy item should load with schema_version=%q, got %q", CurrentSchemaVersion, got.SchemaVersion)
	}
	if got.Title != "legacy item" {
		t.Fatalf("legacy item title: %q", got.Title)
	}
}

// TestStoreReadThenWritePreservesSchemaVersion verifies the load -> save
// cycle preserves the field rather than dropping or rewriting it. This
// matters because most CLI verbs (resolve, reopen, close) follow exactly
// this pattern: Load, mutate, Save. A subtle bug there would silently
// strip schema_version from items modified after install.
func TestStoreReadThenWritePreservesSchemaVersion(t *testing.T) {
	s := tempStore(t)
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	original := NewItem("ask-3c89", "x", UrgencyNormal, StatusOpen, created)
	if err := s.Save(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load("ask-3c89")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != "1" {
		t.Fatalf("loaded schema_version: got %q want %q", loaded.SchemaVersion, "1")
	}
	loaded.Title = "y"
	if err := s.Save(loaded); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root(), "items", "ask-3c89.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"schema_version": "1"`) {
		t.Fatalf("read-then-write should preserve schema_version on disk, got: %s", string(raw))
	}
}

// TestOpenStoreUninitializedNoCfg verifies that opening a directory with no
// .ask/ and no cfg returns a clear error rather than silently creating an
// empty config.
func TestOpenStoreUninitializedNoCfg(t *testing.T) {
	dir := t.TempDir()
	_, err := OpenStore(dir, nil)
	if err == nil {
		t.Fatal("expected error opening uninitialized store with nil cfg, got nil")
	}
}
