package core

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// harvestSeed opens (or creates) a store at dir with the given items
// written through Save. Distinct ProjectID from the target store so id
// hashing is independent — the same shape a worktree-dispatched subagent
// would produce (its own `ask init` mints a fresh project_id).
func harvestSeed(t *testing.T, dir, projectID string, items ...*Item) *FileStore {
	t.Helper()
	cfg := &ProjectConfig{
		ProjectID:   projectID,
		DisplayName: "test",
		CreatedAt:   time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
	}
	store, err := OpenStore(dir, cfg)
	if err != nil {
		t.Fatalf("OpenStore %s: %v", dir, err)
	}
	for _, it := range items {
		if err := store.Save(it); err != nil {
			t.Fatalf("Save %s: %v", it.ID, err)
		}
	}
	return store
}

// TestHarvestCopiesItemsToTarget is the primary acceptance test for
// act-a587: items in source are copied into target with every field
// preserved, and the source files remain untouched when clean=false.
func TestHarvestCopiesItemsToTarget(t *testing.T) {
	srcDir, tgtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	body := "creds at .env.local"
	filedBy := "claude-code:worktree-x"
	tracker := "act-3c89"

	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A",
		&Item{
			ID: "ask-3c89", Title: "OAuth", Body: body,
			Urgency: UrgencyBlocker, Status: StatusOpen,
			FiledBy: &filedBy, TrackerRef: &tracker,
			Verifier:  &Verifier{Type: VerifierShell, Command: "pnpm test:auth", TimeoutSeconds: 60},
			Links:     []Link{{Label: "console", URL: "https://example.com"}},
			CreatedAt: base,
		},
	)
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B")

	res, err := Harvest(target, source, false)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if len(res.Harvested) != 1 || res.Harvested[0] != "ask-3c89" {
		t.Fatalf("Harvested = %v, want [ask-3c89]", res.Harvested)
	}
	if res.Cleaned {
		t.Fatalf("Cleaned = true, want false (clean was not requested)")
	}

	// Item present in target, all fields preserved.
	got, err := target.Load("ask-3c89")
	if err != nil {
		t.Fatalf("target.Load: %v", err)
	}
	if got.Title != "OAuth" || got.Body != body ||
		got.Urgency != UrgencyBlocker || got.Status != StatusOpen {
		t.Fatalf("title/body/urgency/status lost: %+v", got)
	}
	if got.FiledBy == nil || *got.FiledBy != filedBy {
		t.Fatalf("filed_by lost: %v", got.FiledBy)
	}
	if got.TrackerRef == nil || *got.TrackerRef != tracker {
		t.Fatalf("tracker_ref lost: %v", got.TrackerRef)
	}
	if got.Verifier == nil || got.Verifier.Command != "pnpm test:auth" {
		t.Fatalf("verifier lost: %+v", got.Verifier)
	}
	if len(got.Links) != 1 || got.Links[0].URL != "https://example.com" {
		t.Fatalf("links lost: %+v", got.Links)
	}

	// Source still has the item (clean=false).
	if _, err := source.Load("ask-3c89"); err != nil {
		t.Fatalf("source item should remain when clean=false: %v", err)
	}
}

// TestHarvestEmptySourceIsNoop covers the dispatch-no-asks-filed path:
// orchestrator runs harvest unconditionally on every worktree teardown,
// most of which don't file asks. The call must succeed cleanly.
func TestHarvestEmptySourceIsNoop(t *testing.T) {
	srcDir, tgtDir := t.TempDir(), t.TempDir()
	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A")
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B")

	res, err := Harvest(target, source, false)
	if err != nil {
		t.Fatalf("Harvest empty: %v", err)
	}
	if len(res.Harvested) != 0 {
		t.Fatalf("Harvested = %v, want empty", res.Harvested)
	}
	if res.Harvested == nil {
		t.Fatalf("Harvested is nil; want []string{} for non-null JSON output")
	}
}

// TestHarvestCollisionAbortsPreflight verifies the safety guarantee: if
// any source id is already in target, the call errors out without
// modifying either store. (Theoretical at 2^16 id namespace; the test
// forces the collision by direct seeding.)
func TestHarvestCollisionAbortsPreflight(t *testing.T) {
	srcDir, tgtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A",
		&Item{ID: "ask-aaaa", Title: "src", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base, Links: []Link{}},
		&Item{ID: "ask-bbbb", Title: "also-src", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base, Links: []Link{}},
	)
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B",
		&Item{ID: "ask-aaaa", Title: "tgt-already", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base, Links: []Link{}},
	)

	_, err := Harvest(target, source, false)
	if !errors.Is(err, ErrIDCollision) {
		t.Fatalf("expected ErrIDCollision, got %v", err)
	}

	// Target's existing item is unchanged.
	got, err := target.Load("ask-aaaa")
	if err != nil {
		t.Fatalf("target.Load: %v", err)
	}
	if got.Title != "tgt-already" {
		t.Fatalf("target item clobbered: title=%q", got.Title)
	}
	// Source's non-colliding item was NOT copied (all-or-nothing pre-flight).
	if _, err := target.Load("ask-bbbb"); err == nil {
		t.Fatalf("non-colliding source item leaked into target on collision-abort path")
	}
}

// TestHarvestCleanRemovesSource covers the orchestrator-teardown path:
// after a successful copy with --clean, the source .ask/items/ is
// emptied so a re-run is a no-op rather than a collision storm.
func TestHarvestCleanRemovesSource(t *testing.T) {
	srcDir, tgtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A",
		&Item{ID: "ask-cccc", Title: "src", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base, Links: []Link{}},
		&Item{ID: "ask-dddd", Title: "src2", Urgency: UrgencyNormal, Status: StatusOpen, CreatedAt: base, Links: []Link{}},
	)
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B")

	res, err := Harvest(target, source, true)
	if err != nil {
		t.Fatalf("Harvest clean: %v", err)
	}
	if !res.Cleaned {
		t.Fatalf("Cleaned = false, want true")
	}
	sort.Strings(res.Harvested)
	want := []string{"ask-cccc", "ask-dddd"}
	if !equalStrings(res.Harvested, want) {
		t.Fatalf("Harvested = %v, want %v", res.Harvested, want)
	}

	// Source items are gone.
	for _, id := range want {
		path := filepath.Join(srcDir, ".ask", "items", id+".json")
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source item %s should have been removed; stat err = %v", id, err)
		}
	}
	// Source config.json is preserved — only items get cleaned.
	if _, err := os.Stat(filepath.Join(srcDir, ".ask", "config.json")); err != nil {
		t.Fatalf("source config.json should remain: %v", err)
	}
	// A re-harvest finds nothing.
	res2, err := Harvest(target, source, true)
	if err != nil {
		t.Fatalf("second Harvest: %v", err)
	}
	if len(res2.Harvested) != 0 {
		t.Fatalf("re-harvest after clean expected empty, got %v", res2.Harvested)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
