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
