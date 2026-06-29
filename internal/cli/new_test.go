package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// initStore creates a .ask/ directory in dir by calling core.OpenStore
// directly with a fresh ProjectConfig. This decouples cli/new tests from
// the (separately-tracked) `ask init` implementation.
func initStore(t *testing.T, dir string) {
	t.Helper()
	cfg := &core.ProjectConfig{
		ProjectID:   "01HXYZTESTPROJECT00000000",
		DisplayName: "test",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := core.OpenStore(dir, cfg); err != nil {
		t.Fatalf("setup store: %v", err)
	}
}

func TestNewCreatesItem(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "Set up Gmail OAuth", "--body", "do the thing", "--urgency", "blocker"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
}

func TestNewRequiresTitle(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)
	if code := Run([]string{"new"}); code != 2 {
		t.Fatalf("expected 2, got %d", code)
	}
}

func TestNewRefusesWithoutInit(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if code := Run([]string{"new", "x"}); code == 0 {
		t.Fatal("expected error when not initialized")
	}
}

// newSingleItem opens the store at dir and returns the single item written
// to it. Tests that exercise `ask new` through Run can use this to inspect
// the saved Item without parsing stdout.
func newSingleItem(t *testing.T, dir string) *core.Item {
	t.Helper()
	store, err := core.OpenStore(dir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ids, err := store.ListIDs()
	if err != nil {
		t.Fatalf("list ids: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 item, got %d", len(ids))
	}
	it, err := store.Load(ids[0])
	if err != nil {
		t.Fatalf("load %s: %v", ids[0], err)
	}
	return it
}

// TestNewFlagsAfterPositional is the dogfood-caught bug from act-b122:
// `ask new "title" --body "x"` was silently dropping --body because
// flag.Parse stops at the first non-flag argument. The fix is to reorder
// flags-first before parsing. This test pins the regression for both
// --body and --urgency, which have to round-trip through the saved Item.
func TestNewFlagsAfterPositional(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "my title", "--body", "the body", "--urgency", "blocker"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if it.Title != "my title" {
		t.Fatalf("title = %q, want %q", it.Title, "my title")
	}
	if it.Body != "the body" {
		t.Fatalf("body = %q, want %q (flag after positional was dropped)", it.Body, "the body")
	}
	if it.Urgency != core.UrgencyBlocker {
		t.Fatalf("urgency = %q, want blocker (flag after positional was dropped)", it.Urgency)
	}
}

// TestNewFlagsFirstStillWorks is the regression-guard companion to
// TestNewFlagsAfterPositional: the pre-fix flag-first form must keep
// working after we start reordering args.
func TestNewFlagsFirstStillWorks(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "--body", "the body", "--urgency", "blocker", "my title"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if it.Title != "my title" || it.Body != "the body" || it.Urgency != core.UrgencyBlocker {
		t.Fatalf("unexpected item title/body/urgency: title=%q body=%q urgency=%q",
			it.Title, it.Body, it.Urgency)
	}
}

// TestNewBlocksSingle pins that `ask new --blocks <ref>` records the ref
// on the created item.
func TestNewBlocksSingle(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t", "--blocks", "act-3c89"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if len(it.Blocks) != 1 || it.Blocks[0] != "act-3c89" {
		t.Fatalf("blocks = %v, want [act-3c89]", it.Blocks)
	}
}

// TestNewBlocksRepeatable pins that the flag accumulates across
// repetitions in input order — same convention as --status / --urgency.
func TestNewBlocksRepeatable(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t", "--blocks", "act-3c89", "--blocks", "linear-eng-1234"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	want := []string{"act-3c89", "linear-eng-1234"}
	if len(it.Blocks) != len(want) {
		t.Fatalf("blocks length = %d, want %d (%v)", len(it.Blocks), len(want), it.Blocks)
	}
	for i, w := range want {
		if it.Blocks[i] != w {
			t.Fatalf("blocks[%d] = %q, want %q", i, it.Blocks[i], w)
		}
	}
}

// TestNewBlocksRejectsEmptyRef pins the spec validation: a whitespace-only
// or empty `--blocks` argument is a validation error (exit 2). Otherwise
// a typo or shell expansion glitch would silently file a ghost ref.
func TestNewBlocksRejectsEmptyRef(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t", "--blocks", "   "}); code != 2 {
		t.Fatalf("whitespace-only --blocks should exit 2, got %d", code)
	}
	if code := Run([]string{"new", "t", "--blocks", ""}); code != 2 {
		t.Fatalf("empty --blocks should exit 2, got %d", code)
	}
}

// TestNewBlocksRejectsOverlongRef pins the per-ref length cap. The cap
// is exposed for spec parity (docs/spec.md §1.1).
func TestNewBlocksRejectsOverlongRef(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	long := strings.Repeat("a", maxBlocksRefLen+1)
	if code := Run([]string{"new", "t", "--blocks", long}); code != 2 {
		t.Fatalf("overlong --blocks should exit 2, got %d", code)
	}
}

// TestNewBlocksDefaultEmpty pins that omitting --blocks leaves the
// item's Blocks field as the empty slice (always-present, [] when none —
// spec §1.1 forward-compat for downstream parsers).
func TestNewBlocksDefaultEmpty(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if it.Blocks == nil {
		t.Fatalf("blocks should be non-nil empty slice, got nil")
	}
	if len(it.Blocks) != 0 {
		t.Fatalf("blocks should be empty, got %v", it.Blocks)
	}
}

// TestNewRecipientSet pins that `ask new --recipient <ref>` records the
// ref on the created item. The field is optional and ask never
// interprets the format; this confirms the flag round-trips through to
// the stored item.
func TestNewRecipientSet(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t", "--recipient", "agent:data-prep"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if it.Recipient == nil || *it.Recipient != "agent:data-prep" {
		t.Fatalf("recipient = %+v, want agent:data-prep", it.Recipient)
	}
}

// TestNewRecipientDefaultNil pins that omitting --recipient leaves the
// Recipient field nil (absent = implicit human, per the design brief).
func TestNewRecipientDefaultNil(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t"}); code != 0 {
		t.Fatalf("new exit: %d", code)
	}
	it := newSingleItem(t, dir)
	if it.Recipient != nil {
		t.Fatalf("recipient should be nil when --recipient omitted, got %+v", it.Recipient)
	}
}

// TestNewRecipientRejectsWhitespace pins the validation analog to
// `--blocks`: whitespace-only or empty values exit 2.
func TestNewRecipientRejectsWhitespace(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	if code := Run([]string{"new", "t", "--recipient", "   "}); code != 2 {
		t.Fatalf("whitespace-only --recipient should exit 2, got %d", code)
	}
}

// TestNewRecipientRejectsOverlong pins the per-ref length cap (1024).
func TestNewRecipientRejectsOverlong(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	long := strings.Repeat("a", maxRecipientRefLen+1)
	if code := Run([]string{"new", "t", "--recipient", long}); code != 2 {
		t.Fatalf("overlong --recipient should exit 2, got %d", code)
	}
}

// TestNewJSONFlagAfterPositional pins the bool-flag form of the same bug:
// `ask new "title" --json` must emit JSON. (`--json` is a boolean flag, so
// reorderFlagsFirst's value-greediness needs to not eat the title.)
func TestNewJSONFlagAfterPositional(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"new", "my title", "--json"}) })
	if code != 0 {
		t.Fatalf("new --json exit: %d, out=%q", code, out)
	}
	// JSON output begins with `{`; plaintext id output is a bare ask-XXXX.
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("expected JSON output, got: %q", out)
	}
}
