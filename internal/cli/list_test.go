package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// seedStore opens (or creates) an .ask/ directory at dir and writes each
// item directly through the core store. Tests use this instead of
// Run([]string{"new", ...}) because `ask new` is a stub until task 9 lands;
// going through the store keeps these tests independent of that future
// implementation.
func seedStore(t *testing.T, dir string, items ...*core.Item) {
	t.Helper()
	cfg := &core.ProjectConfig{
		ProjectID:   "01HXTEST00000000000000000A",
		DisplayName: "test",
		CreatedAt:   time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
	}
	store, err := core.OpenStore(dir, cfg)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	for _, it := range items {
		if err := store.Save(it); err != nil {
			t.Fatalf("Save %s: %v", it.ID, err)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote. Used to inspect `ask list` output without parsing
// the surrounding test harness's logs.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	os.Stdout = orig
	return <-done
}

// inDir chdirs into dir for the duration of the test (restored via t.Cleanup).
// The CLI's runList / runShow read os.Getwd() to locate .ask/, so callers
// that want to drive them against a t.TempDir() must chdir into it.
func inDir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

// TestListShowsOpenByDefault is the acceptance test from act-93f6: a bare
// `ask list` must succeed (exit 0), and must omit closed items while
// including blocker/normal items in spec §3.2 order (blocker first).
func TestListShowsOpenByDefault(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-0001", Title: "alpha", Urgency: core.UrgencyBlocker, Status: core.StatusOpen, CreatedAt: base},
		&core.Item{ID: "ask-0002", Title: "beta", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base.Add(time.Hour)},
		&core.Item{ID: "ask-0003", Title: "gamma-closed", Urgency: core.UrgencyBlocker, Status: core.StatusClosed, CreatedAt: base.Add(2 * time.Hour)},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}

	// alpha (blocker, open) before beta (normal, open); gamma-closed hidden.
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output:\n%s", out)
	}
	if !strings.Contains(out, "beta") {
		t.Fatalf("expected beta in output:\n%s", out)
	}
	if strings.Contains(out, "gamma-closed") {
		t.Fatalf("closed item leaked into default list:\n%s", out)
	}
	if i, j := strings.Index(out, "alpha"), strings.Index(out, "beta"); i < 0 || j < 0 || i > j {
		t.Fatalf("expected blocker (alpha) before normal (beta):\n%s", out)
	}

	// Spec §3.3: tab-separated columns. Verify the alpha row has the
	// expected shape: id, urgency, status, verifier-marker, title.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.Contains(line, "alpha") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 5 {
			t.Fatalf("alpha row should have 5 tab-separated columns, got %d: %q", len(cols), line)
		}
		if cols[0] != "ask-0001" {
			t.Fatalf("alpha row col0 (id) = %q, want ask-0001", cols[0])
		}
		if strings.TrimSpace(cols[1]) != "blocker" {
			t.Fatalf("alpha row col1 (urgency) = %q, want blocker", cols[1])
		}
		if strings.TrimSpace(cols[2]) != "open" {
			t.Fatalf("alpha row col2 (status) = %q, want open", cols[2])
		}
		if strings.TrimSpace(cols[3]) != "" {
			t.Fatalf("alpha row col3 (verifier-marker) = %q, want blank (no verifier)", cols[3])
		}
		if cols[4] != "alpha" {
			t.Fatalf("alpha row col4 (title) = %q, want alpha", cols[4])
		}
	}
}

// TestListJSON is the acceptance test from act-93f6: `ask list --json` must
// succeed and emit a JSON array of Item objects in spec §3.2 order. An
// empty result set must be `[]`, never `null`.
func TestListJSON(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-00a1", Title: "alpha", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "--json"}) })
	if code != 0 {
		t.Fatalf("list --json exit: %d", code)
	}

	var got []core.Item
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a valid JSON array of Items: %v\nout=%q", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item in JSON array, got %d", len(got))
	}
	if got[0].ID != "ask-00a1" || got[0].Title != "alpha" {
		t.Fatalf("unexpected item in JSON array: %+v", got[0])
	}
}

// TestListJSONEmptyIsArrayNotNull covers the spec §1.4 requirement that an
// empty match set serializes as `[]`, not `null`. Otherwise downstream
// agents would have to branch on key presence (and Go's nil slice marshals
// as `null` by default).
func TestListJSONEmptyIsArrayNotNull(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir) // no items
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "--json"}) })
	if code != 0 {
		t.Fatalf("list --json exit: %d", code)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed != "[]" {
		t.Fatalf("expected empty JSON array \"[]\", got %q", trimmed)
	}
}

// TestListStatusFilterRepeatable exercises spec §3.4: `--status` is
// repeatable with OR semantics, and an explicit `--status` overrides the
// default (not-closed) filter.
func TestListStatusFilterRepeatable(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{ID: "ask-00b1", Title: "open-one", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base},
		&core.Item{ID: "ask-00b2", Title: "resolved-one", Urgency: core.UrgencyNormal, Status: core.StatusResolved, CreatedAt: base.Add(time.Hour)},
		&core.Item{ID: "ask-00b3", Title: "closed-one", Urgency: core.UrgencyNormal, Status: core.StatusClosed, CreatedAt: base.Add(2 * time.Hour)},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"list", "--status", "resolved", "--status", "closed"})
	})
	if code != 0 {
		t.Fatalf("list --status exit: %d", code)
	}
	if strings.Contains(out, "open-one") {
		t.Fatalf("open item leaked when --status was resolved,closed:\n%s", out)
	}
	if !strings.Contains(out, "resolved-one") || !strings.Contains(out, "closed-one") {
		t.Fatalf("expected resolved+closed items, got:\n%s", out)
	}
}

// TestListBadStatusEnum guards the spec §3.4 promise that bad enum values
// produce exit 2 (validation error), not a silent empty result.
func TestListBadStatusEnum(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	inDir(t, dir)

	code := Run([]string{"list", "--status", "bogus"})
	if code != 2 {
		t.Fatalf("expected exit 2 for invalid --status, got %d", code)
	}
}

// TestListBlocksFilterHit pins that `ask list --blocks <ref>` includes
// items whose Blocks array contains the ref and excludes those that
// don't. Exact match, OR semantics when repeated.
func TestListBlocksFilterHit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-bb01", Title: "wants-act", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base,
			Blocks: []string{"act-3c89", "linear-eng-1234"},
		},
		&core.Item{
			ID: "ask-bb02", Title: "wants-linear", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(time.Hour),
			Blocks: []string{"linear-eng-9999"},
		},
		&core.Item{
			ID: "ask-bb03", Title: "no-blocks", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(2 * time.Hour),
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "--blocks", "act-3c89"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(out, "wants-act") {
		t.Fatalf("expected wants-act in --blocks=act-3c89 output:\n%s", out)
	}
	if strings.Contains(out, "wants-linear") {
		t.Fatalf("wants-linear should not match --blocks=act-3c89:\n%s", out)
	}
	if strings.Contains(out, "no-blocks") {
		t.Fatalf("no-blocks should not match --blocks=act-3c89:\n%s", out)
	}
}

// TestListBlocksFilterMiss pins that a filter ref no item carries yields
// the empty result set rather than an error or a false positive.
func TestListBlocksFilterMiss(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-bc01", Title: "alpha", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base, Blocks: []string{"act-aaaa"},
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "--blocks", "act-zzzz"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

// TestListBlocksFilterRepeatable pins OR semantics across repeats — same
// convention --status and --urgency use.
func TestListBlocksFilterRepeatable(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-bd01", Title: "alpha", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base, Blocks: []string{"act-1111"},
		},
		&core.Item{
			ID: "ask-bd02", Title: "beta", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(time.Hour), Blocks: []string{"act-2222"},
		},
		&core.Item{
			ID: "ask-bd03", Title: "gamma", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(2 * time.Hour),
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"list", "--blocks", "act-1111", "--blocks", "act-2222"})
	})
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected alpha+beta in --blocks=...,... output:\n%s", out)
	}
	if strings.Contains(out, "gamma") {
		t.Fatalf("gamma (no blocks) should not match:\n%s", out)
	}
}

// TestListBlocksComposesWithStatus is the AND-with-other-filters
// acceptance check: a --blocks filter must intersect with --status.
func TestListBlocksComposesWithStatus(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-be01", Title: "open-match", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base, Blocks: []string{"act-3c89"},
		},
		&core.Item{
			ID: "ask-be02", Title: "resolved-match", Urgency: core.UrgencyNormal,
			Status: core.StatusResolved, CreatedAt: base.Add(time.Hour), Blocks: []string{"act-3c89"},
		},
		&core.Item{
			ID: "ask-be03", Title: "open-other-ref", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(2 * time.Hour), Blocks: []string{"act-zzzz"},
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"list", "--status", "open", "--blocks", "act-3c89"})
	})
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(out, "open-match") {
		t.Fatalf("expected open-match in AND output:\n%s", out)
	}
	if strings.Contains(out, "resolved-match") {
		t.Fatalf("resolved-match should be filtered by --status=open:\n%s", out)
	}
	if strings.Contains(out, "open-other-ref") {
		t.Fatalf("open-other-ref should be filtered by --blocks=act-3c89:\n%s", out)
	}
}

// TestListBlocksRejectsEmpty pins that a whitespace-only --blocks filter
// surfaces as a validation error (exit 2) rather than a silent
// match-nothing.
func TestListBlocksRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	inDir(t, dir)

	if code := Run([]string{"list", "--blocks", "  "}); code != 2 {
		t.Fatalf("expected exit 2 for whitespace --blocks, got %d", code)
	}
}

// TestListRecipientFilterHit pins that `ask list --recipient <ref>`
// includes items whose Recipient equals the ref and excludes items
// without that recipient (including the implicit-human nil case).
func TestListRecipientFilterHit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	dprep := "agent:data-prep"
	other := "agent:other"
	seedStore(t, dir,
		&core.Item{
			ID: "ask-r001", Title: "for-dprep", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base, Recipient: &dprep,
		},
		&core.Item{
			ID: "ask-r002", Title: "for-other", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(time.Hour), Recipient: &other,
		},
		&core.Item{
			ID: "ask-r003", Title: "no-recipient", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(2 * time.Hour),
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "--recipient", "agent:data-prep"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(out, "for-dprep") {
		t.Fatalf("expected for-dprep in --recipient=agent:data-prep output:\n%s", out)
	}
	if strings.Contains(out, "for-other") {
		t.Fatalf("for-other should not match --recipient=agent:data-prep:\n%s", out)
	}
	if strings.Contains(out, "no-recipient") {
		t.Fatalf("no-recipient (nil) should not match an explicit --recipient filter:\n%s", out)
	}
}

// TestListRecipientFilterRepeatable pins OR semantics across repeated
// --recipient flags (mirrors --status / --urgency / --blocks).
func TestListRecipientFilterRepeatable(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	a := "agent:a"
	b := "agent:b"
	c := "agent:c"
	seedStore(t, dir,
		&core.Item{
			ID: "ask-r101", Title: "alpha", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base, Recipient: &a,
		},
		&core.Item{
			ID: "ask-r102", Title: "beta", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(time.Hour), Recipient: &b,
		},
		&core.Item{
			ID: "ask-r103", Title: "gamma", Urgency: core.UrgencyNormal,
			Status: core.StatusOpen, CreatedAt: base.Add(2 * time.Hour), Recipient: &c,
		},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"list", "--recipient", "agent:a", "--recipient", "agent:b"})
	})
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected alpha+beta in OR output:\n%s", out)
	}
	if strings.Contains(out, "gamma") {
		t.Fatalf("gamma should not match repeated --recipient set:\n%s", out)
	}
}

// TestListRecipientRejectsEmpty pins that a whitespace-only --recipient
// filter surfaces as a validation error (exit 2) rather than a silent
// match-nothing.
func TestListRecipientRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	inDir(t, dir)

	if code := Run([]string{"list", "--recipient", "  "}); code != 2 {
		t.Fatalf("expected exit 2 for whitespace --recipient, got %d", code)
	}
}

// TestListVerifierMarker checks that the spec §3.3 verifier column reads
// "V" for an item with a verifier attached and a single space otherwise.
func TestListVerifierMarker(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		&core.Item{
			ID: "ask-00c1", Title: "with-v", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base,
			Verifier: &core.Verifier{Type: core.VerifierShell, Command: "echo ok", TimeoutSeconds: 30},
		},
		&core.Item{ID: "ask-00c2", Title: "without-v", Urgency: core.UrgencyNormal, Status: core.StatusOpen, CreatedAt: base.Add(time.Hour)},
	)
	inDir(t, dir)

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list"}) })
	if code != 0 {
		t.Fatalf("list exit: %d", code)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) != 5 {
			t.Fatalf("expected 5 tab-separated columns, got %d in line %q", len(cols), line)
		}
		switch cols[0] {
		case "ask-00c1":
			if cols[3] != "V" {
				t.Fatalf("with-v row verifier marker = %q, want %q", cols[3], "V")
			}
		case "ask-00c2":
			if cols[3] != " " {
				t.Fatalf("without-v row verifier marker = %q, want single space", cols[3])
			}
		}
	}
}
