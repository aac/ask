package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// act-fac3f6: ask_list's 1024 cap on its `recipient` and `blocks` filters
// counts characters (Unicode code points), as ask_new's does — so an item
// filed with a 1024-character non-ASCII ref can be filtered for.

// TestListfixRefFiltersCountCharacters: a 1024-non-ASCII-character filter
// returns the item filed with that ref; 1025 is refused with code 2.
func TestListfixRefFiltersCountCharacters(t *testing.T) {
	for _, field := range []string{"recipient", "blocks"} {
		for _, ch := range []string{"é", "日"} {
			root := t.TempDir()
			initRepo(t, root)
			ref := strings.Repeat(ch, 1024)
			var newArgs map[string]any
			if field == "blocks" {
				newArgs = map[string]any{"title": "t", field: []string{ref}}
			} else {
				newArgs = map[string]any{"title": "t", field: ref}
			}
			if tr := mcpdriftCall(t, root, "ask_new", newArgs); tr.IsError {
				t.Fatalf("%s %q: ask_new refused: %.200s", field, ch, tr.Content[0].Text)
			}
			if tr := mcpdriftCall(t, root, "ask_new", map[string]any{"title": "other"}); tr.IsError {
				t.Fatalf("ask_new (unrelated item) refused: %.200s", tr.Content[0].Text)
			}

			tr := mcpdriftCall(t, root, "ask_list", map[string]any{field: ref})
			if tr.IsError {
				t.Fatalf("%s %q x1024: filter refused: %.200s", field, ch, tr.Content[0].Text)
			}
			var items []core.Item
			if err := json.Unmarshal([]byte(tr.Content[0].Text), &items); err != nil {
				t.Fatalf("%s %q x1024: result is not an item array: %v", field, ch, err)
			}
			if len(items) != 1 || items[0].Title != "t" {
				t.Fatalf("%s %q x1024: want exactly the item filed with the ref, got %d item(s)", field, ch, len(items))
			}

			tr = mcpdriftCall(t, root, "ask_list", map[string]any{field: ref + ch})
			if code := mcpdriftErrCode(t, tr); code != 2 {
				t.Fatalf("%s %q x1025: code %d, want 2", field, ch, code)
			}
			if msg := tr.Content[0].Text; !strings.Contains(msg, "ask list: "+field+": ref exceeds 1024 characters") {
				t.Fatalf("%s %q x1025: unexpected message: %.200s", field, ch, msg)
			}
		}
	}
}
