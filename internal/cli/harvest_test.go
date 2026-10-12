package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// harvestSeedAt initializes a .ask/ store under root and writes the
// given items, returning the absolute root path. Distinct project_id
// mimics the worktree-vs-main setup the harvest verb is designed for.
func harvestSeedAt(t *testing.T, root, projectID string, items ...*core.Item) string {
	t.Helper()
	cfg := &core.ProjectConfig{
		ProjectID:   projectID,
		DisplayName: filepath.Base(root),
		CreatedAt:   time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
	}
	store, err := core.OpenStore(root, cfg)
	if err != nil {
		t.Fatalf("OpenStore %s: %v", root, err)
	}
	for _, it := range items {
		if err := store.Save(it); err != nil {
			t.Fatalf("Save %s: %v", it.ID, err)
		}
	}
	return root
}

// TestHarvestCLISurfacesWorktreeAsk is the acceptance test mapping to
// act-a587: a subagent files into a worktree's .ask/; the orchestrator
// runs `ask harvest --from <worktree>` from main; the human's main-side
// `ask list` then includes the worktree-filed item.
func TestHarvestCLISurfacesWorktreeAsk(t *testing.T) {
	mainDir := t.TempDir()
	worktreeDir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)

	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	harvestSeedAt(t, worktreeDir, "01HXWORK0000000000000000B",
		&core.Item{
			ID: "ask-aaaa", Title: "wt-filed",
			Urgency: core.UrgencyBlocker, Status: core.StatusOpen,
			CreatedAt: base, Links: []core.Link{},
		},
	)
	inDir(t, mainDir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"harvest", "--from", worktreeDir}) })
	if code != 0 {
		t.Fatalf("harvest exit: %d, out=%q", code, out)
	}
	if !strings.Contains(out, "ask-aaaa") {
		t.Fatalf("expected harvested id in stdout: %q", out)
	}

	// Verify by going through the same path the human would: `ask list`.
	listOut := captureStdout(t, func() { code = Run([]string{"list"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(listOut, "ask-aaaa") || !strings.Contains(listOut, "wt-filed") {
		t.Fatalf("main `ask list` should show harvested item; got:\n%s", listOut)
	}
}

// TestHarvestCLIRequiresFromFlag pins the flag-missing → exit 2 contract.
func TestHarvestCLIRequiresFromFlag(t *testing.T) {
	dir := t.TempDir()
	harvestSeedAt(t, dir, "01HXTEST00000000000000000A")
	inDir(t, dir)

	if code := Run([]string{"harvest"}); code != 2 {
		t.Fatalf("expected exit 2 when --from missing, got %d", code)
	}
}

// TestHarvestCLISourceMissingStoreExits5 covers the "pointed at the wrong
// directory" case: the path exists but has no .ask/ — same uninitialized
// signal core.OpenStore raises for the target.
func TestHarvestCLISourceMissingStoreExits5(t *testing.T) {
	mainDir := t.TempDir()
	emptyDir := t.TempDir() // no .ask/ at all
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	inDir(t, mainDir)

	if code := Run([]string{"harvest", "--from", emptyDir}); code != 5 {
		t.Fatalf("expected exit 5 for uninitialized source, got %d", code)
	}
}

// TestHarvestCLISelfHarvestExits2 refuses a no-op self-harvest as a
// validation error rather than letting it surface as a collision storm.
func TestHarvestCLISelfHarvestExits2(t *testing.T) {
	dir := t.TempDir()
	harvestSeedAt(t, dir, "01HXTEST00000000000000000A")
	inDir(t, dir)

	if code := Run([]string{"harvest", "--from", dir}); code != 2 {
		t.Fatalf("expected exit 2 for self-harvest, got %d", code)
	}
}

// TestHarvestCLICollisionExits5 verifies the pre-flight collision check
// surfaces as exit 5 and leaves both stores untouched.
func TestHarvestCLICollisionExits5(t *testing.T) {
	mainDir, wtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A",
		&core.Item{ID: "ask-eeee", Title: "main", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base, Links: []core.Link{}},
	)
	harvestSeedAt(t, wtDir, "01HXWORK0000000000000000B",
		&core.Item{ID: "ask-eeee", Title: "worktree-same-id", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base, Links: []core.Link{}},
	)
	inDir(t, mainDir)

	if code := Run([]string{"harvest", "--from", wtDir}); code != 5 {
		t.Fatalf("expected exit 5 on id collision, got %d", code)
	}
	// Main's original item is unchanged.
	store, err := core.OpenStore(mainDir, nil)
	if err != nil {
		t.Fatalf("OpenStore mainDir: %v", err)
	}
	it, err := store.Load("ask-eeee")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if it.Title != "main" {
		t.Fatalf("main item title was clobbered on collision: %q", it.Title)
	}
}

// TestHarvestCLICleanRemovesSource verifies --clean removes source items
// after the copy and that a second run finds nothing to harvest.
func TestHarvestCLICleanRemovesSource(t *testing.T) {
	mainDir, wtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	harvestSeedAt(t, wtDir, "01HXWORK0000000000000000B",
		&core.Item{ID: "ask-ffff", Title: "wt", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base, Links: []core.Link{}},
	)
	inDir(t, mainDir)

	if code := Run([]string{"harvest", "--from", wtDir, "--clean"}); code != 0 {
		t.Fatalf("harvest --clean exit: %d", code)
	}
	// Source item is gone.
	if _, err := os.Stat(filepath.Join(wtDir, ".ask", "items", "ask-ffff.json")); err == nil {
		t.Fatalf("source item should have been removed by --clean")
	}
	// Re-running is a clean no-op (exit 0, "nothing to harvest").
	var code int
	out := captureStdout(t, func() { code = Run([]string{"harvest", "--from", wtDir, "--clean"}) })
	if code != 0 {
		t.Fatalf("re-harvest exit: %d", code)
	}
	if !strings.Contains(out, "nothing to harvest") {
		t.Fatalf("re-harvest expected 'nothing to harvest' message, got: %q", out)
	}
}

// TestHarvestCLIJSONEmitsSummary verifies the --json output shape so a
// machine-driven orchestrator can act on the harvested ids.
func TestHarvestCLIJSONEmitsSummary(t *testing.T) {
	mainDir, wtDir := t.TempDir(), t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	harvestSeedAt(t, wtDir, "01HXWORK0000000000000000B",
		&core.Item{ID: "ask-1111", Title: "a", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base, Links: []core.Link{}},
		&core.Item{ID: "ask-2222", Title: "b", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base, Links: []core.Link{}},
	)
	inDir(t, mainDir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"harvest", "--from", wtDir, "--json"}) })
	if code != 0 {
		t.Fatalf("harvest --json exit: %d, out=%q", code, out)
	}
	var got harvestJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json parse: %v\nout=%q", err, out)
	}
	if got.From != wtDir {
		t.Fatalf("From = %q, want %q", got.From, wtDir)
	}
	if got.Cleaned {
		t.Fatalf("Cleaned = true, want false (no --clean)")
	}
	if len(got.Harvested) != 2 {
		t.Fatalf("Harvested = %v, want 2 entries", got.Harvested)
	}
	// Sorted by core.Harvest.
	if got.Harvested[0] != "ask-1111" || got.Harvested[1] != "ask-2222" {
		t.Fatalf("Harvested out of order: %v", got.Harvested)
	}
}

// TestHarvestCLIEmptySourceJSONShape pins the empty-list serialization:
// `[]` not `null` so consumer scripts don't have to branch on JSON null.
func TestHarvestCLIEmptySourceJSONShape(t *testing.T) {
	mainDir, wtDir := t.TempDir(), t.TempDir()
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	harvestSeedAt(t, wtDir, "01HXWORK0000000000000000B")
	inDir(t, mainDir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"harvest", "--from", wtDir, "--json"}) })
	if code != 0 {
		t.Fatalf("harvest exit: %d", code)
	}
	if strings.Contains(out, `"harvested": null`) || strings.Contains(out, `"harvested":null`) {
		t.Fatalf("empty harvested list should serialize as [], got null:\n%s", out)
	}
	if !strings.Contains(out, `"harvested": []`) && !strings.Contains(out, `"harvested":[]`) {
		t.Fatalf("expected harvested:[] in JSON output, got:\n%s", out)
	}
}

// harvestatomicSnapshot returns filename → bytes for every entry in a
// store's items directory (empty map when the directory is absent), so a
// test can assert a store is byte-identical before and after a verb.
func harvestatomicSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, ".ask", "items")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]string{}
	}
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	snap := make(map[string]string, len(entries))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", e.Name(), err)
		}
		snap[e.Name()] = string(b)
	}
	return snap
}

// harvestatomicSameSnapshot fails the test when two snapshots differ in
// file set or content.
func harvestatomicSameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s changed: %d file(s) before, %d after (%v)", what, len(before), len(after), harvestatomicNames(after))
	}
	for name, b := range before {
		a, ok := after[name]
		if !ok {
			t.Fatalf("%s: %s was removed", what, name)
		}
		if a != b {
			t.Fatalf("%s: %s was rewritten", what, name)
		}
	}
}

func harvestatomicNames(snap map[string]string) []string {
	names := make([]string, 0, len(snap))
	for n := range snap {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// harvestatomicBreak makes one source item unreadable in the named way:
// "corrupt" truncates it to invalid JSON; "schema" keeps it a valid item
// but hand-sets schema_version to "2" (spec §1: unreadable to this binary).
func harvestatomicBreak(t *testing.T, root, id, kind string) {
	t.Helper()
	path := filepath.Join(root, ".ask", "items", id+".json")
	switch kind {
	case "corrupt":
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", id, err)
		}
	case "schema":
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatalf("decode %s: %v", id, err)
		}
		raw["schema_version"] = "2"
		out, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			t.Fatalf("encode %s: %v", id, err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	default:
		t.Fatalf("unknown break kind %q", kind)
	}
}

// TestHarvestCLIUnreadableSourceCopiesNothing pins act-f80d7a: when any
// source item is unreadable, harvest fails with exit 5 having written
// nothing to the target and removed nothing from the source — even with
// --clean, and even for the readable items that sort before the bad one.
// Two unreadable items (middle and last) check that every one is named,
// not just the first hit.
func TestHarvestCLIUnreadableSourceCopiesNothing(t *testing.T) {
	for _, kind := range []string{"corrupt", "schema"} {
		t.Run(kind, func(t *testing.T) {
			mainDir := t.TempDir()
			worktreeDir := t.TempDir()
			base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
			mk := func(id string) *core.Item {
				return &core.Item{
					ID: id, Title: "t-" + id,
					Urgency: core.UrgencyNormal, Status: core.StatusOpen,
					CreatedAt: base, Links: []core.Link{},
				}
			}
			// The target already holds an unrelated item: "unchanged" must
			// mean its existing contents survive too, not merely "empty".
			harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A", mk("ask-0000"))
			harvestSeedAt(t, worktreeDir, "01HXWORK0000000000000000B",
				mk("ask-aaaa"), mk("ask-bbbb"), mk("ask-cccc"), mk("ask-dddd"))
			harvestatomicBreak(t, worktreeDir, "ask-bbbb", kind)
			harvestatomicBreak(t, worktreeDir, "ask-dddd", kind)
			inDir(t, mainDir)

			targetBefore := harvestatomicSnapshot(t, mainDir)
			sourceBefore := harvestatomicSnapshot(t, worktreeDir)

			var code int
			var stdout string
			stderr := captureStderr(t, func() {
				stdout = captureStdout(t, func() {
					code = Run([]string{"harvest", "--from", worktreeDir, "--clean"})
				})
			})
			if code != 5 {
				t.Fatalf("exit: got %d, want 5 (stderr=%q)", code, stderr)
			}
			harvestatomicSameSnapshot(t, "target", targetBefore, harvestatomicSnapshot(t, mainDir))
			harvestatomicSameSnapshot(t, "source", sourceBefore, harvestatomicSnapshot(t, worktreeDir))
			if stdout != "" {
				t.Fatalf("failed harvest must print nothing on stdout; got %q", stdout)
			}
			for _, id := range []string{"ask-bbbb", "ask-dddd"} {
				if !strings.Contains(stderr, id) {
					t.Fatalf("stderr should name every unreadable id; missing %s in %q", id, stderr)
				}
			}
			if kind == "schema" && !strings.Contains(stderr, "schema_version") {
				t.Fatalf("stderr should name schema_version: %q", stderr)
			}
		})
	}
}
