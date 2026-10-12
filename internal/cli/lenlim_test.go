package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aac/ask/internal/core"
)

// act-58c1b4: the title limit is 200 characters (Unicode code points),
// not 200 bytes, and the CLI deliberately applies no cap to the body.

// lenlimStore makes a temp dir with an empty store and chdirs into it.
func lenlimStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	initStore(t, dir)
	return dir
}

// lenlimItems loads every item in the store.
func lenlimItems(t *testing.T, dir string) []*core.Item {
	t.Helper()
	store, err := core.OpenStore(dir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	items, _, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return items
}

// TestNewTitleLimitCountsCharacters: 200 non-ASCII characters (400 or
// 600 bytes) is a valid title; 201 is refused with exit 2 and nothing is
// written.
func TestNewTitleLimitCountsCharacters(t *testing.T) {
	for _, ch := range []string{"é", "日"} {
		dir := lenlimStore(t)
		if code := Run([]string{"new", strings.Repeat(ch, 201)}); code != 2 {
			t.Fatalf("%q x201: exit %d, want 2", ch, code)
		}
		if n := len(lenlimItems(t, dir)); n != 0 {
			t.Fatalf("%q x201: refused title still wrote %d item(s)", ch, n)
		}
		if code := Run([]string{"new", strings.Repeat(ch, 200)}); code != 0 {
			t.Fatalf("%q x200: exit %d, want 0", ch, code)
		}
		items := lenlimItems(t, dir)
		if len(items) != 1 {
			t.Fatalf("%q x200: %d item(s), want 1", ch, len(items))
		}
		if got := utf8.RuneCountInString(items[0].Title); got != 200 {
			t.Fatalf("%q x200: stored title is %d characters", ch, got)
		}
	}
}

// TestNewTitleStillRefusesNewlines: `ask new` now takes its length and
// newline rules from core.ValidateTitle; the newline rule must survive.
func TestNewTitleStillRefusesNewlines(t *testing.T) {
	dir := lenlimStore(t)
	if code := Run([]string{"new", "line one\nline two"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if n := len(lenlimItems(t, dir)); n != 0 {
		t.Fatalf("refused title still wrote %d item(s)", n)
	}
}

// TestUpdateTitleLimitCountsCharacters is the same boundary on
// `ask update --title`.
func TestUpdateTitleLimitCountsCharacters(t *testing.T) {
	dir := seedOne(t, "ask-7a7a", "old", "body")
	if code := Run([]string{"update", "ask-7a7a", "--title", strings.Repeat("é", 201)}); code != 2 {
		t.Fatalf("201 characters: exit %d, want 2", code)
	}
	if got := loadItem(t, dir, "ask-7a7a").Title; got != "old" {
		t.Fatalf("refused title was written: %q", got)
	}
	if code := Run([]string{"update", "ask-7a7a", "--title", strings.Repeat("é", 200)}); code != 0 {
		t.Fatalf("200 characters: exit %d, want 0", code)
	}
	if got := utf8.RuneCountInString(loadItem(t, dir, "ask-7a7a").Title); got != 200 {
		t.Fatalf("stored title is %d characters", got)
	}
}

// TestCLIBodyIsUnbounded pins the decision that the CLI applies no body
// cap (spec §1.1): an ask refused at filing time is an ask that is lost.
// The 16384 limit belongs to the MCP boundary only.
func TestCLIBodyIsUnbounded(t *testing.T) {
	dir := lenlimStore(t)
	long := strings.Repeat("x", 16385)
	if code := Run([]string{"new", "long body", "--body", long}); code != 0 {
		t.Fatalf("new with a 16385-character body: exit %d, want 0", code)
	}
	items := lenlimItems(t, dir)
	if len(items) != 1 || items[0].Body != long {
		t.Fatalf("long body was not stored intact")
	}
	id := items[0].ID
	if code := Run([]string{"update", id, "--body-append", long}); code != 0 {
		t.Fatalf("update --body-append past 16384: exit %d, want 0", code)
	}
	if got := len(loadItem(t, dir, id).Body); got != 2*16385+2 {
		t.Fatalf("body after append is %d bytes, want %d", got, 2*16385+2)
	}
}
