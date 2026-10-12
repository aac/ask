package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aac/ask/internal/core"
)

// act-5dd2e1: ask_new's 1024 cap on `recipient` and on each `blocks` ref
// counts characters (Unicode code points), the way its schema's
// maxLength reads and the way the CLI counts the same fields.

// TestNewRefCapsCountCharacters: 1024 non-ASCII characters is accepted
// and stored whole; 1025 is refused with code 2 and nothing is written.
func TestNewRefCapsCountCharacters(t *testing.T) {
	for _, field := range []string{"recipient", "blocks"} {
		for _, ch := range []string{"é", "日"} {
			root := t.TempDir()
			initRepo(t, root)
			newlimArg := func(n int) map[string]any {
				v := strings.Repeat(ch, n)
				if field == "blocks" {
					return map[string]any{"title": "t", field: []string{v}}
				}
				return map[string]any{"title": "t", field: v}
			}

			before := mcpdriftSnapshot(t, root)
			tr := mcpdriftCall(t, root, "ask_new", newlimArg(1025))
			if code := mcpdriftErrCode(t, tr); code != 2 {
				t.Fatalf("%s %q x1025: code %d, want 2", field, ch, code)
			}
			if msg := tr.Content[0].Text; !strings.Contains(msg, field) || !strings.Contains(msg, "exceeds 1024 characters") {
				t.Fatalf("%s %q x1025: message does not name the field and the limit: %.200s", field, ch, msg)
			}
			mcpdriftSameStore(t, root, before)

			tr = mcpdriftCall(t, root, "ask_new", newlimArg(1024))
			if tr.IsError {
				t.Fatalf("%s %q x1024: refused: %.200s", field, ch, tr.Content[0].Text)
			}
			var it core.Item
			if err := json.Unmarshal([]byte(tr.Content[0].Text), &it); err != nil {
				t.Fatalf("%s %q x1024: result is not an item: %v", field, ch, err)
			}
			var stored string
			switch {
			case field == "recipient" && it.Recipient != nil:
				stored = *it.Recipient
			case field == "blocks" && len(it.Blocks) == 1:
				stored = it.Blocks[0]
			}
			if got := utf8.RuneCountInString(stored); got != 1024 {
				t.Fatalf("%s %q x1024: stored value is %d characters, want 1024", field, ch, got)
			}
		}
	}
}
