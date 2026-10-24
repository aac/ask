package mcp

import (
	"encoding/json"
	"testing"

	"github.com/aac/ask/internal/core"
)

// TestAskNewBlocksActDepPart pins act-58f80e on the MCP side: content[0]
// is still the created Item, and each act-shaped `blocks` ref adds one
// text part carrying the `act dep add` command, the same line `ask new`
// writes to stderr. A non-act ref adds nothing.
func TestAskNewBlocksActDepPart(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 300, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":  "blocking the act task",
				"blocks": []string{"act-3c89", "linear-eng-1234"},
			},
		},
	})
	tr := extractToolResult(t, resp)
	if tr.IsError || len(tr.Content) != 2 {
		t.Fatalf("want success with 2 content parts, got %+v", tr)
	}
	var created core.Item
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &created); err != nil {
		t.Fatalf("content[0] is not the item: %v\n%s", err, tr.Content[0].Text)
	}
	want := core.ActDepHintPrefix + "act dep add act-3c89 --external " + created.ID
	if tr.Content[1].Type != "text" || tr.Content[1].Text != want {
		t.Fatalf("content[1] = %+v, want text %q", tr.Content[1], want)
	}
	if tr.Meta != nil {
		t.Fatalf("ask_new must carry no _meta, got %v", tr.Meta)
	}

	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 301, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "no act ref", "blocks": []string{"linear-eng-1234"}},
		},
	})
	if tr := extractToolResult(t, resp); tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("non-act blocks: want one content part, got %+v", tr)
	}
}
