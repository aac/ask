package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// TestShowResolvesPrefix exercises the §4.3 prefix-resolution path: a
// short, unambiguous hex prefix (with or without the `ask-` prefix and in
// either case) resolves to the full id and `ask show` exits 0.
func TestShowResolvesPrefix(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-3c89", Title: "alpha", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "3c"}) })
	if code != 0 {
		t.Fatalf("show 3c exit: %d", code)
	}
	if !strings.Contains(out, "ask-3c89") || !strings.Contains(out, "alpha") {
		t.Fatalf("expected id and title in show output, got:\n%s", out)
	}
}

// TestShowJSONEmitsFullItem covers spec §1.5: `--json` emits the full Item
// object as a single JSON value with every field populated.
func TestShowJSONEmitsFullItem(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	resolvedAt := base.Add(time.Hour)
	seedStore(t, dir,
		&core.Item{
			ID:                 "ask-7ecd",
			Title:              "beta",
			Body:               "body content",
			Urgency:            core.UrgencyBlocker,
			Status:             core.StatusResolved,
			ResolutionNote:     ptrString("handled"),
			VerificationOutput: ptrString("all green"),
			CreatedAt:          base,
			ResolvedAt:         &resolvedAt,
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "--json", "ask-7ecd"}) })
	if code != 0 {
		t.Fatalf("show --json exit: %d", code)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a valid JSON Item: %v\nout=%q", err, out)
	}
	if got.ID != "ask-7ecd" || got.Title != "beta" {
		t.Fatalf("unexpected item: %+v", got)
	}
	if got.ResolutionNote == nil || *got.ResolutionNote != "handled" {
		t.Fatalf("unexpected resolution_note: %v", got.ResolutionNote)
	}
}

// ptrString is a small helper so test seeds can stay terse.
func ptrString(s string) *string { return &s }

// TestShowNotFoundExits3 verifies the spec §2 exit-code mapping for a
// prefix that matches zero items.
func TestShowNotFoundExits3(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	inDir(t, dir)

	code := Run([]string{"show", "deadbeef"})
	if code != 3 {
		t.Fatalf("expected exit 3 for not-found id, got %d", code)
	}
}

// TestShowAmbiguousPrefixExits4 verifies the spec §2 exit-code mapping for
// a prefix that matches more than one id.
func TestShowAmbiguousPrefixExits4(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-aaa1", Title: "one", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base},
		&core.Item{ID: "ask-aaa2", Title: "two", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base.Add(time.Hour)},
	)
	inDir(t, dir)

	code := Run([]string{"show", "aaa"})
	if code != 4 {
		t.Fatalf("expected exit 4 for ambiguous prefix, got %d", code)
	}
}

// TestShowJSONFlagAfterPositional is the dogfood-caught bug from act-b122:
// `ask show <id> --json` was silently emitting plaintext because flag.Parse
// stops at the first non-flag argument. The fix is to reorder flags-first
// before parsing.
func TestShowJSONFlagAfterPositional(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-7ecd", Title: "beta", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "ask-7ecd", "--json"}) })
	if code != 0 {
		t.Fatalf("show <id> --json exit: %d, out=%q", code, out)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("expected JSON output when --json follows the id, got plaintext: %v\nout=%q", err, out)
	}
	if got.ID != "ask-7ecd" {
		t.Fatalf("unexpected item id: %q", got.ID)
	}
}

// TestShowRendersBlocksText pins that the plain-text `ask show` output
// surfaces each blocks ref under a "Blocks:" heading. Empty blocks emit
// nothing in text form (matches the Links/Verifier conventions).
func TestShowRendersBlocksText(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-c011", Title: "with-blocks", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base,
			Blocks: []string{"act-3c89", "linear-eng-1234"},
		},
		&core.Item{
			ID: "ask-c012", Title: "no-blocks", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(time.Hour),
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "ask-c011"}) })
	if code != 0 {
		t.Fatalf("show with-blocks exit: %d", code)
	}
	if !strings.Contains(out, "Blocks:") {
		t.Fatalf("expected 'Blocks:' heading in text output:\n%s", out)
	}
	if !strings.Contains(out, "act-3c89") || !strings.Contains(out, "linear-eng-1234") {
		t.Fatalf("expected both blocks refs in output:\n%s", out)
	}

	out = captureStdout(t, func() { code = Run([]string{"show", "ask-c012"}) })
	if code != 0 {
		t.Fatalf("show no-blocks exit: %d", code)
	}
	if strings.Contains(out, "Blocks:") {
		t.Fatalf("'Blocks:' heading should be suppressed when empty:\n%s", out)
	}
}

// TestShowJSONIncludesBlocks pins that `ask show --json` always emits
// the blocks field (even when empty) so downstream parsers never have
// to branch on key presence.
func TestShowJSONIncludesBlocks(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-c021", Title: "with-blocks", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base,
			Blocks: []string{"act-3c89"},
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "--json", "ask-c021"}) })
	if code != 0 {
		t.Fatalf("show --json exit: %d", code)
	}
	if !strings.Contains(out, `"blocks":`) {
		t.Fatalf("expected blocks key in JSON output:\n%s", out)
	}
	if !strings.Contains(out, `"act-3c89"`) {
		t.Fatalf("expected ref string in blocks JSON:\n%s", out)
	}
	var got core.Item
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nout=%q", err, out)
	}
	if len(got.Blocks) != 1 || got.Blocks[0] != "act-3c89" {
		t.Fatalf("blocks round-trip: got %v", got.Blocks)
	}
}

// TestShowNoIDExits2 verifies that calling `ask show` with no argument is
// a validation error (exit 2), not a not-found error.
func TestShowNoIDExits2(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	inDir(t, dir)

	code := Run([]string{"show"})
	if code != 2 {
		t.Fatalf("expected exit 2 when id missing, got %d", code)
	}
}
