package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aac/ask/internal/core"
)

// act-58c1b4: the MCP length caps are limits on the ARGUMENT a client
// sends, not on what is stored. The CLI files bodies of any length, so
// an item can already be longer than 16384 characters when an MCP
// client appends to it; that append must succeed.

// lenlimSetBody writes body straight into the stored item, the way a CLI
// `ask new --body` / `ask update --body` of any length would.
func lenlimSetBody(t *testing.T, root, id, body string) {
	t.Helper()
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	it, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load %s: %v", id, err)
	}
	it.Body = body
	if err := store.Save(it); err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
}

// TestBodyAppendToAlreadyLongBody: a within-limit body_append to an item
// whose stored body is already past 16384 characters is accepted and
// lands after the blank-line separator.
func TestBodyAppendToAlreadyLongBody(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "short", 1)
	long := strings.Repeat("x", 16385)
	lenlimSetBody(t, root, it.ID, long)

	tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "a short note"})
	if tr.IsError {
		t.Fatalf("append to an already-long body was refused: %.200s", tr.Content[0].Text)
	}
	if got, want := mcpupdShow(t, root, it.ID, 2).Body, long+"\n\na short note"; got != want {
		t.Fatalf("body after append: %d characters, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}

	// The argument itself is still capped, whatever the stored length.
	before := mcpdriftSnapshot(t, root)
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": strings.Repeat("y", 16385)})
	mcpdriftWantTooLong(t, tr, "body_append", 16384)
	mcpdriftSameStore(t, root, before)
}

// TestTitleLimitCountsCharactersNotBytes: the title's maxLength of 200
// is characters on both tools that take one, so a 200-character title
// in a multi-byte script is accepted and 201 is refused.
func TestTitleLimitCountsCharactersNotBytes(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	tr := mcpdriftCall(t, root, "ask_new", map[string]any{"title": strings.Repeat("日", 200)})
	if tr.IsError {
		t.Fatalf("ask_new: a 200-character non-ASCII title was refused: %.200s", tr.Content[0].Text)
	}
	tr = mcpdriftCall(t, root, "ask_new", map[string]any{"title": strings.Repeat("日", 201)})
	if !tr.IsError || mcpdriftErrCode(t, tr) != 2 {
		t.Fatalf("ask_new: a 201-character title was not refused with code 2: %.200s", tr.Content[0].Text)
	}

	it := mcpupdNew(t, root, "seed", "", 3)
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "title": strings.Repeat("é", 200)})
	if tr.IsError {
		t.Fatalf("ask_update: a 200-character non-ASCII title was refused: %.200s", tr.Content[0].Text)
	}
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "title": strings.Repeat("é", 201)})
	if !tr.IsError || mcpdriftErrCode(t, tr) != 2 {
		t.Fatalf("ask_update: a 201-character title was not refused with code 2: %.200s", tr.Content[0].Text)
	}
}

// TestBodyAppendCeilingOnStoredBody: the result of a body_append is
// bounded at maxAppendedBodyLen characters, so a client appending
// within-limit chunks in a loop cannot grow an item forever. The
// near-limit body is written straight into the store; no loop needed.
func TestBodyAppendCeilingOnStoredBody(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "short", 1)
	// Multi-byte characters: the ceiling counts code points, not bytes.
	// An append costs the two-character separator plus the text, so
	// (max-3) + 2 + 1 lands exactly on the ceiling.
	lenlimSetBody(t, root, it.ID, strings.Repeat("é", maxAppendedBodyLen-3))

	tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "y"})
	if tr.IsError {
		t.Fatalf("append landing exactly on the ceiling was refused: %.200s", tr.Content[0].Text)
	}
	if got := utf8.RuneCountInString(mcpupdShow(t, root, it.ID, 2).Body); got != maxAppendedBodyLen {
		t.Fatalf("body after append: %d characters, want %d", got, maxAppendedBodyLen)
	}

	before := mcpdriftSnapshot(t, root)
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "z"})
	if !tr.IsError || mcpdriftErrCode(t, tr) != 2 {
		t.Fatalf("append past the ceiling was not refused with code 2: %.200s", tr.Content[0].Text)
	}
	want := "ask update: body_append would grow body past 1048576 characters (body is 1048576 now, would be 1048579)"
	if !strings.Contains(tr.Content[0].Text, want) {
		t.Fatalf("message does not name the field and the limit:\n got: %.300s\nwant: %s", tr.Content[0].Text, want)
	}
	mcpdriftSameStore(t, root, before)

	// A title-only correction of an item at the ceiling is still allowed.
	if tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "title": "renamed"}); tr.IsError {
		t.Fatalf("title-only update of an item at the ceiling was refused: %.200s", tr.Content[0].Text)
	}
}
