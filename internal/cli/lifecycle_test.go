package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// TestResolveReopenClose drives a single item through resolve -> reopen ->
// close via the public Run dispatcher and asserts (a) every transition
// returns exit 0, (b) the on-disk Item ends up in the expected status with
// the expected resolution_note / verification_output carried through.
//
// The seed item is constructed via internal/core directly rather than via
// `ask new`, so this test does not depend on Task 9 / runNew being
// implemented. That keeps the Task 11 acceptance gate self-contained.
func TestResolveReopenClose(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Seed: open store + write one open item via core. We don't need a real
	// ULID for the project_id — core treats it as an opaque string.
	cfg := &core.ProjectConfig{
		ProjectID:   "01TESTTESTTESTTESTTESTTEST",
		DisplayName: "lifecycle-test",
		CreatedAt:   time.Now().UTC(),
	}
	store, err := core.OpenStore(dir, cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now().UTC()
	id, err := core.NewID(cfg.ProjectID, now, "alpha", func(string) bool { return false })
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	seed := &core.Item{
		ID:        id,
		Title:     "alpha",
		Urgency:   core.UrgencyNormal,
		Status:    core.StatusOpen,
		CreatedAt: now,
	}
	if err := store.Save(seed); err != nil {
		t.Fatalf("save seed: %v", err)
	}

	// Sanity: one item on disk.
	files, _ := os.ReadDir(filepath.Join(dir, ".ask", "items"))
	if len(files) != 1 {
		t.Fatalf("expected 1 item, got %d", len(files))
	}

	// resolve
	if code := Run([]string{"resolve", id, "--note", "did it"}); code != 0 {
		t.Fatalf("resolve exit: %d", code)
	}
	got, err := store.Load(id)
	if err != nil {
		t.Fatalf("load after resolve: %v", err)
	}
	if got.Status != core.StatusResolved {
		t.Fatalf("status after resolve = %s, want resolved", got.Status)
	}
	if got.ResolutionNote == nil || *got.ResolutionNote != "did it" {
		t.Fatalf("resolution_note after resolve = %v, want %q", got.ResolutionNote, "did it")
	}
	if got.ResolvedAt == nil {
		t.Fatalf("resolved_at not set after resolve")
	}

	// reopen
	if code := Run([]string{"reopen", id, "--reason", "verifier failed"}); code != 0 {
		t.Fatalf("reopen exit: %d", code)
	}
	got, err = store.Load(id)
	if err != nil {
		t.Fatalf("load after reopen: %v", err)
	}
	if got.Status != core.StatusOpen {
		t.Fatalf("status after reopen = %s, want open", got.Status)
	}
	if got.VerificationOutput == nil || *got.VerificationOutput != "verifier failed" {
		t.Fatalf("verification_output after reopen = %v, want %q", got.VerificationOutput, "verifier failed")
	}
	if got.ResolvedAt != nil {
		t.Fatalf("resolved_at should be cleared after reopen")
	}

	// close
	if code := Run([]string{"close", id}); code != 0 {
		t.Fatalf("close exit: %d", code)
	}
	got, err = store.Load(id)
	if err != nil {
		t.Fatalf("load after close: %v", err)
	}
	if got.Status != core.StatusClosed {
		t.Fatalf("status after close = %s, want closed", got.Status)
	}
	if got.ClosedAt == nil {
		t.Fatalf("closed_at not set after close")
	}
}

// TestMutateExitCodes covers the mutate() helper's error taxonomy:
//   - missing id arg → 2 (validation)
//   - unknown id prefix → 3 (not found)
//   - ambiguous id prefix → 4 (ambiguous)
//   - resolve on closed (invalid transition) → 2 (validation)
func TestMutateExitCodes(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg := &core.ProjectConfig{
		ProjectID:   "01TESTTESTTESTTESTTESTTEST",
		DisplayName: "exit-codes",
		CreatedAt:   time.Now().UTC(),
	}
	store, err := core.OpenStore(dir, cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	// missing id → 2
	if code := Run([]string{"resolve"}); code != 2 {
		t.Fatalf("resolve no-args exit: %d, want 2", code)
	}

	// not found → 3
	if code := Run([]string{"resolve", "deadbeef"}); code != 3 {
		t.Fatalf("resolve unknown-id exit: %d, want 3", code)
	}

	// Seed two items whose ids share a 3-char prefix so we can force a
	// guaranteed ambiguous-match scenario regardless of the hash output.
	a := &core.Item{
		ID:        "ask-aaaa",
		Title:     "a",
		Urgency:   core.UrgencyNormal,
		Status:    core.StatusOpen,
		CreatedAt: time.Now().UTC(),
	}
	b := &core.Item{
		ID:        "ask-aaab",
		Title:     "b",
		Urgency:   core.UrgencyNormal,
		Status:    core.StatusOpen,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Save(a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := store.Save(b); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if code := Run([]string{"resolve", "aaa"}); code != 4 {
		t.Fatalf("resolve ambiguous-prefix exit: %d, want 4", code)
	}

	// Invalid transition: resolve on a closed item is exit 2.
	c := &core.Item{
		ID:        "ask-cccc",
		Title:     "c",
		Urgency:   core.UrgencyNormal,
		Status:    core.StatusClosed,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Save(c); err != nil {
		t.Fatalf("save c: %v", err)
	}
	if code := Run([]string{"resolve", "cccc"}); code != 2 {
		t.Fatalf("resolve on closed exit: %d, want 2", code)
	}
}
