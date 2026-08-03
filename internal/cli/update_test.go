package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// seedOne stores a single open item with the given id/title/body and
// returns the directory it lives in, already chdir'd to.
func seedOne(t *testing.T, id, title, body string) string {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	it := core.NewItem(id, title, core.UrgencyNormal, core.StatusOpen, time.Now().UTC())
	it.Body = body
	seedStore(t, dir, it)
	return dir
}

func loadItem(t *testing.T, dir, id string) *core.Item {
	t.Helper()
	store, err := core.OpenStore(dir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	it, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load %s: %v", id, err)
	}
	return it
}

// TestUpdateBodyAppend is the case act-82bf7a was filed for: an ask whose
// text has rotted gets a dated correction appended — while staying open.
// The alternative routes were editing .ask/items/<id>.json by hand or
// resolve-and-refile, the latter of which closes an ask that still needs
// the human.
func TestUpdateBodyAppend(t *testing.T) {
	dir := seedOne(t, "ask-1111", "Set up OAuth", "Run the dogfood tomorrow.")

	out := captureStdout(t, func() {
		if code := Run([]string{"update", "ask-1111", "--body-append", "2026-08-02: still open; 'tomorrow' meant 2026-05-14."}); code != 0 {
			t.Fatalf("update exit %d, want 0", code)
		}
	})
	if !strings.Contains(out, "ask-1111: updated") {
		t.Fatalf("stdout %q", out)
	}

	it := loadItem(t, dir, "ask-1111")
	want := "Run the dogfood tomorrow.\n\n2026-08-02: still open; 'tomorrow' meant 2026-05-14."
	if it.Body != want {
		t.Fatalf("body:\n got %q\nwant %q", it.Body, want)
	}
	if it.Status != core.StatusOpen {
		t.Fatalf("update must not transition status, got %s", it.Status)
	}
	if it.Title != "Set up OAuth" {
		t.Fatalf("title should be untouched, got %q", it.Title)
	}
	if it.ResolvedAt != nil || it.ClosedAt != nil {
		t.Fatal("update must not stamp lifecycle timestamps")
	}
}

// An append onto an empty body must not leave a leading blank line.
func TestUpdateBodyAppendOntoEmptyBody(t *testing.T) {
	dir := seedOne(t, "ask-2222", "t", "")
	if code := Run([]string{"update", "ask-2222", "--body-append", "first note"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := loadItem(t, dir, "ask-2222").Body; got != "first note" {
		t.Fatalf("body %q", got)
	}
}

func TestUpdateBodyReplaceAndClear(t *testing.T) {
	dir := seedOne(t, "ask-3333", "t", "old body")

	if code := Run([]string{"update", "ask-3333", "--body", "new body"}); code != 0 {
		t.Fatalf("replace: exit %d", code)
	}
	if got := loadItem(t, dir, "ask-3333").Body; got != "new body" {
		t.Fatalf("body %q", got)
	}

	// An explicit empty string clears, matching `act update --description ""`.
	if code := Run([]string{"update", "ask-3333", "--body", ""}); code != 0 {
		t.Fatalf("clear: exit %d", code)
	}
	if got := loadItem(t, dir, "ask-3333").Body; got != "" {
		t.Fatalf("body should be cleared, got %q", got)
	}
}

func TestUpdateTitle(t *testing.T) {
	dir := seedOne(t, "ask-4444", "old title", "body")
	if code := Run([]string{"update", "ask-4444", "--title", "  new title  "}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	it := loadItem(t, dir, "ask-4444")
	if it.Title != "new title" {
		t.Fatalf("title %q", it.Title)
	}
	if it.Body != "body" {
		t.Fatalf("body should be untouched, got %q", it.Body)
	}
}

func TestUpdateBodyFileAndStdin(t *testing.T) {
	dir := seedOne(t, "ask-5555", "t", "old")
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("from a file"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if code := Run([]string{"update", "ask-5555", "--body-file", path}); code != 0 {
		t.Fatalf("--body-file exit %d", code)
	}
	if got := loadItem(t, dir, "ask-5555").Body; got != "from a file" {
		t.Fatalf("body %q", got)
	}

	// `-` reads stdin, matching `act update --description-file -`.
	withStdinText(t, "appended from stdin", func() {
		if code := Run([]string{"update", "ask-5555", "--body-append-file", "-"}); code != 0 {
			t.Fatalf("--body-append-file - exit %d", code)
		}
	})
	if got := loadItem(t, dir, "ask-5555").Body; got != "from a file\n\nappended from stdin" {
		t.Fatalf("body %q", got)
	}
}

func TestUpdateValidationErrors(t *testing.T) {
	dir := seedOne(t, "ask-6666", "t", "body")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no fields", []string{"update", "ask-6666"}, 2},
		{"no id", []string{"update", "--body", "x"}, 2},
		{"body and body-append", []string{"update", "ask-6666", "--body", "x", "--body-append", "y"}, 2},
		{"body and body-file", []string{"update", "ask-6666", "--body", "x", "--body-file", "/nope"}, 2},
		{"empty title", []string{"update", "ask-6666", "--title", "   "}, 2},
		{"newline in title", []string{"update", "ask-6666", "--title", "a\nb"}, 2},
		{"long title", []string{"update", "ask-6666", "--title", strings.Repeat("x", 201)}, 2},
		{"unknown id", []string{"update", "ask-9999", "--body", "x"}, 3},
		{"missing body file", []string{"update", "ask-6666", "--body-file", filepath.Join(dir, "absent.txt")}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			captureStderr(t, func() { code = Run(tc.args) })
			if code != tc.want {
				t.Fatalf("exit %d, want %d", code, tc.want)
			}
		})
	}

	// None of the failures may have touched the item.
	it := loadItem(t, dir, "ask-6666")
	if it.Title != "t" || it.Body != "body" {
		t.Fatalf("failed updates mutated the item: %q / %q", it.Title, it.Body)
	}
}

// TestUpdateAmbiguousPrefix pins exit 4 on the shared prefix path.
func TestUpdateAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedStore(t, dir,
		core.NewItem("ask-ab11", "one", core.UrgencyNormal, core.StatusOpen, time.Now().UTC()),
		core.NewItem("ask-ab22", "two", core.UrgencyNormal, core.StatusOpen, time.Now().UTC()),
	)
	var code int
	captureStderr(t, func() { code = Run([]string{"update", "ask-ab", "--body", "x"}) })
	if code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
}

// TestUpdateNoOp: asking for text the item already has is an idempotent
// no-op — exit 6 with a stderr warning, the success shape still on stdout,
// and no write (so mtime is untouched), matching the resolve/reopen/close
// envelope in spec §1.8.
func TestUpdateNoOp(t *testing.T) {
	dir := seedOne(t, "ask-7777", "same title", "same body")
	path := filepath.Join(dir, ".ask", "items", "ask-7777.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	var code int
	var out string
	stderr := captureStderr(t, func() {
		out = captureStdout(t, func() {
			code = Run([]string{"update", "ask-7777", "--title", "same title", "--body", "same body"})
		})
	})
	if code != 6 {
		t.Fatalf("exit %d, want 6", code)
	}
	if !strings.Contains(stderr, "already reads as requested") {
		t.Fatalf("stderr %q", stderr)
	}
	if !strings.Contains(out, "ask-7777") {
		t.Fatalf("stdout should still emit the success shape, got %q", out)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op update rewrote the item file")
	}
}

// Updating a closed item is allowed: correcting the record of something
// already handled is not a state change, and refusing it would push agents
// back to editing the JSON by hand.
func TestUpdateWorksOnClosedItem(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	it := core.NewItem("ask-8888", "done thing", core.UrgencyNormal, core.StatusClosed, time.Now().UTC())
	seedStore(t, dir, it)

	if code := Run([]string{"update", "ask-8888", "--body-append", "postmortem note"}); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	got := loadItem(t, dir, "ask-8888")
	if got.Status != core.StatusClosed {
		t.Fatalf("status changed to %s", got.Status)
	}
	if !strings.Contains(got.Body, "postmortem note") {
		t.Fatalf("body %q", got.Body)
	}
}

func TestUpdateJSONOutput(t *testing.T) {
	seedOne(t, "ask-9999", "t", "old")
	out := captureStdout(t, func() {
		if code := Run([]string{"update", "ask-9999", "--body", "new", "--json"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
	})
	if !strings.Contains(out, `"body": "new"`) || !strings.Contains(out, `"id": "ask-9999"`) {
		t.Fatalf("json output %q", out)
	}
}

// withStdinText runs fn with os.Stdin backed by a pipe carrying input.
func withStdinText(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	go func() {
		_, _ = w.WriteString(input)
		_ = w.Close()
	}()
	defer func() { os.Stdin = orig }()
	fn()
}
