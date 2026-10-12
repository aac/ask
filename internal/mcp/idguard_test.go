package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// idguardSeedMCP creates a store holding ask-cccc (ordinary) and a file
// ask-aaaa.json whose "id" member says ask-bbbb (act-7ee74d). Returns the
// mismatched file's path and exact bytes.
func idguardSeedMCP(t *testing.T, root string) (path string, want []byte) {
	t.Helper()
	initRepo(t, root)
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ask-aaaa", "ask-cccc"} {
		if err := store.Save(core.NewItem(id, "probe", core.UrgencyNormal, core.StatusOpen, time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	path = filepath.Join(root, ".ask", "items", "ask-aaaa.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const from = `"id": "ask-aaaa"`
	if !bytes.Contains(b, []byte(from)) {
		t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
	}
	want = bytes.Replace(b, []byte(from), []byte(`"id": "ask-bbbb"`), 1)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, want
}

// TestIdguardMCPToolsRefuse: an item file whose id does not equal its
// filename stem is unreadable over MCP too — code 5 naming both ids, file
// byte-identical — and ask_list skips it with a warning.
func TestIdguardMCPToolsRefuse(t *testing.T) {
	root := t.TempDir()
	path, want := idguardSeedMCP(t, root)

	for i, tool := range []string{"ask_show", "ask_resolve", "ask_close", "ask_reopen"} {
		resp := driveRequest(t, root, map[string]any{
			"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": map[string]any{"id": "ask-aaaa"}},
		})
		tr := extractToolResult(t, resp)
		if !tr.IsError || len(tr.Content) == 0 {
			t.Fatalf("%s: want isError with content, got %+v", tool, tr)
		}
		var env errEnvelope
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
			t.Fatalf("%s: decode envelope: %v\nbody: %s", tool, err, tr.Content[0].Text)
		}
		if env.Code != 5 || !strings.Contains(env.Message, "ask-aaaa") || !strings.Contains(env.Message, "ask-bbbb") {
			t.Fatalf("%s: want code 5 naming both ids, got %+v", tool, env)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s rewrote the item:\n%s", tool, got)
		}
	}

	tr := listskipCall(t, root, map[string]any{})
	if tr.IsError {
		t.Fatalf("ask_list: unexpected error %+v", tr)
	}
	if ids := listskipItems(t, tr); len(ids) != 1 || ids[0] != "ask-cccc" {
		t.Fatalf("ask_list items = %v, want [ask-cccc]", ids)
	}
	if len(tr.Content) != 2 || !strings.Contains(tr.Content[1].Text, "skipped ask-aaaa:") {
		t.Fatalf("ask_list: want one skip warning for ask-aaaa, got %+v", tr.Content)
	}
}
