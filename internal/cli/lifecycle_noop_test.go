package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// seedLifecycleItem opens a store in dir and persists a single item in the
// requested status. Returns the item's id. Used by the idempotent-no-op
// tests below so each case can drive an in-target-state item through the
// CLI dispatcher.
func seedLifecycleItem(t *testing.T, dir string, status core.Status) string {
	t.Helper()
	cfg := &core.ProjectConfig{
		ProjectID:   "01TESTTESTTESTTESTTESTTEST",
		DisplayName: "lifecycle-noop",
		CreatedAt:   time.Now().UTC(),
	}
	store, err := core.OpenStore(dir, cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now().UTC()
	id, err := core.NewID(cfg.ProjectID, now, "noop-seed", func(string) bool { return false })
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	it := &core.Item{
		ID:        id,
		Title:     "noop-seed",
		Urgency:   core.UrgencyNormal,
		Status:    status,
		CreatedAt: now,
	}
	if status == core.StatusResolved {
		t := now
		it.ResolvedAt = &t
	}
	if status == core.StatusClosed {
		t := now
		it.ClosedAt = &t
	}
	if err := store.Save(it); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	return id
}

// TestResolveOnResolvedIsNoOp covers spec §1.8 + §2 exit code 6 for
// `ask resolve` on an already-resolved item:
//   - exit 6
//   - stderr warning matching "ask resolve: already resolved"
//   - stdout still emits the success shape (id) in plain mode.
func TestResolveOnResolvedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusResolved)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"resolve", id})
		})
	})
	if code != 6 {
		t.Fatalf("resolve no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask resolve: already resolved") {
		t.Fatalf("expected stderr warning to mention 'already resolved', got: %q", stderr)
	}
	// Stdout success shape in plain mode: per spec §1.8 it's a one-line
	// success summary. We accept either the active "id: prev -> next" form
	// or the idempotent "id: already resolved" form, both must include
	// the id so scripts piping stdout keep working.
	if !strings.Contains(stdout, id) {
		t.Fatalf("expected stdout to include id %q, got: %q", id, stdout)
	}
}

// TestResolveOnResolvedNoOpJSON covers the --json branch of the same case:
// exit 6 + stderr warning + the unchanged Item object on stdout.
func TestResolveOnResolvedNoOpJSON(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusResolved)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"resolve", id, "--json"})
		})
	})
	if code != 6 {
		t.Fatalf("resolve --json no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask resolve: already resolved") {
		t.Fatalf("expected stderr warning, got: %q", stderr)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.ID != id {
		t.Fatalf("stdout Item id = %q, want %q", got.ID, id)
	}
	if got.Status != core.StatusResolved {
		t.Fatalf("stdout Item status = %s, want resolved", got.Status)
	}
}

// TestCloseOnClosedIsNoOp covers spec §1.8 + §2 exit code 6 for `ask close`
// on an already-closed item.
func TestCloseOnClosedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusClosed)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"close", id})
		})
	})
	if code != 6 {
		t.Fatalf("close no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask close: already closed") {
		t.Fatalf("expected stderr 'already closed' warning, got: %q", stderr)
	}
	if !strings.Contains(stdout, id) {
		t.Fatalf("expected stdout to include id %q, got: %q", id, stdout)
	}
}

// TestCloseOnClosedNoOpJSON: --json variant for closed -> close.
func TestCloseOnClosedNoOpJSON(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusClosed)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"close", id, "--json"})
		})
	})
	if code != 6 {
		t.Fatalf("close --json no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask close: already closed") {
		t.Fatalf("expected stderr warning, got: %q", stderr)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.Status != core.StatusClosed {
		t.Fatalf("stdout Item status = %s, want closed", got.Status)
	}
}

// TestReopenOnOpenIsNoOp covers spec §1.8 + §2 exit code 6 for `ask reopen`
// on an already-open item.
func TestReopenOnOpenIsNoOp(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusOpen)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"reopen", id})
		})
	})
	if code != 6 {
		t.Fatalf("reopen no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask reopen: already open") {
		t.Fatalf("expected stderr 'already open' warning, got: %q", stderr)
	}
	if !strings.Contains(stdout, id) {
		t.Fatalf("expected stdout to include id %q, got: %q", id, stdout)
	}
}

// TestReopenOnOpenNoOpJSON: --json variant for open -> reopen.
func TestReopenOnOpenNoOpJSON(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusOpen)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"reopen", id, "--json"})
		})
	})
	if code != 6 {
		t.Fatalf("reopen --json no-op exit: %d (want 6)", code)
	}
	if !strings.Contains(stderr, "ask reopen: already open") {
		t.Fatalf("expected stderr warning, got: %q", stderr)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.Status != core.StatusOpen {
		t.Fatalf("stdout Item status = %s, want open", got.Status)
	}
}

// TestResolveJSONSuccessActive verifies the non-idempotent --json path:
// resolving an open item exits 0 and emits the post-transition Item.
// Complements the no-op JSON test by pinning the active-transition path.
func TestResolveJSONSuccessActive(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusOpen)

	var code int
	stdout := captureStdout(t, func() {
		code = Run([]string{"resolve", id, "--json", "--note", "done"})
	})
	if code != 0 {
		t.Fatalf("resolve --json active exit: %d (want 0)", code)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.Status != core.StatusResolved {
		t.Fatalf("status = %s, want resolved", got.Status)
	}
	if got.ResolutionNote == nil || *got.ResolutionNote != "done" {
		t.Fatalf("resolution_note = %v, want %q", got.ResolutionNote, "done")
	}
}

// TestCloseJSONSuccessActive verifies the non-idempotent --json path for
// `ask close`: closing a resolved item exits 0 with no stderr warning and
// emits the post-transition Item on stdout. Pairs with the close no-op
// JSON test so a future per-verb divergence in the active-transition
// JSON shape can't regress silently. All three lifecycle verbs delegate
// through mutate(), so this also serves as a behavioural pin on that
// shared helper for the close verb.
func TestCloseJSONSuccessActive(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusResolved)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"close", id, "--json", "--reason", "shipped"})
		})
	})
	if code != 0 {
		t.Fatalf("close --json active exit: %d (want 0)", code)
	}
	if strings.Contains(stderr, "already") {
		t.Fatalf("unexpected stderr warning on active close: %q", stderr)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.ID != id {
		t.Fatalf("stdout Item id = %q, want %q", got.ID, id)
	}
	if got.Status != core.StatusClosed {
		t.Fatalf("status = %s, want closed", got.Status)
	}
	if got.ResolutionNote == nil || *got.ResolutionNote != "shipped" {
		t.Fatalf("resolution_note = %v, want %q", got.ResolutionNote, "shipped")
	}
	if got.ClosedAt == nil {
		t.Fatalf("closed_at not set on post-transition Item")
	}
}

// TestReopenJSONSuccessActive verifies the non-idempotent --json path for
// `ask reopen`: reopening a resolved item exits 0 with no stderr warning
// and emits the post-transition Item on stdout. Pairs with the reopen
// no-op JSON test so a future per-verb divergence in the active JSON
// shape can't regress silently. Complements the resolve/close active
// JSON tests as the third leg of mutate()'s shared active-path coverage.
func TestReopenJSONSuccessActive(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	id := seedLifecycleItem(t, dir, core.StatusResolved)

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"reopen", id, "--json", "--reason", "verifier failed"})
		})
	})
	if code != 0 {
		t.Fatalf("reopen --json active exit: %d (want 0)", code)
	}
	if strings.Contains(stderr, "already") {
		t.Fatalf("unexpected stderr warning on active reopen: %q", stderr)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout was not a JSON Item: %v\n%s", err, stdout)
	}
	if got.ID != id {
		t.Fatalf("stdout Item id = %q, want %q", got.ID, id)
	}
	if got.Status != core.StatusOpen {
		t.Fatalf("status = %s, want open", got.Status)
	}
	if got.VerificationOutput == nil || *got.VerificationOutput != "verifier failed" {
		t.Fatalf("verification_output = %v, want %q", got.VerificationOutput, "verifier failed")
	}
	if got.ResolvedAt != nil {
		t.Fatalf("resolved_at should be cleared on reopen, got: %v", got.ResolvedAt)
	}
}
