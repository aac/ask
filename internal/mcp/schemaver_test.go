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

// schemaverSeedMCP creates a store with one v1 item and one item
// hand-edited to schema_version "2" plus a key v1 does not know. Returns
// the v2 file's path and exact bytes.
func schemaverSeedMCP(t *testing.T, root string) (path string, want []byte) {
	t.Helper()
	initRepo(t, root)
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ask-aaaa", "ask-bbbb"} {
		if err := store.Save(core.NewItem(id, "probe", core.UrgencyNormal, core.StatusOpen, time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	path = filepath.Join(root, ".ask", "items", "ask-bbbb.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const from = `"schema_version": "1",`
	if !bytes.Contains(b, []byte(from)) {
		t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
	}
	want = bytes.Replace(b, []byte(from),
		[]byte(`"schema_version": "2",`+"\n"+`  "subscriptions": ["human:someone"],`), 1)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, want
}

// TestSchemaverMCPToolsRefuse drives the real MCP tools over a store
// holding a schema_version "2" item (spec §1.1.1): ask_show, ask_list and
// every mutating tool return an isError envelope with code 5 naming
// schema_version, and the item file is byte-identical afterwards.
func TestSchemaverMCPToolsRefuse(t *testing.T) {
	root := t.TempDir()
	path, want := schemaverSeedMCP(t, root)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"ask_show", map[string]any{"id": "ask-bbbb"}},
		{"ask_list", map[string]any{}},
		{"ask_resolve", map[string]any{"id": "ask-bbbb"}},
		{"ask_close", map[string]any{"id": "ask-bbbb"}},
		{"ask_reopen", map[string]any{"id": "ask-bbbb"}},
	}
	for i, c := range calls {
		resp := driveRequest(t, root, map[string]any{
			"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]any{"name": c.tool, "arguments": c.args},
		})
		tr := extractToolResult(t, resp)
		if !tr.IsError || len(tr.Content) == 0 {
			t.Fatalf("%s: want isError with content, got %+v", c.tool, tr)
		}
		var env errEnvelope
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
			t.Fatalf("%s: decode envelope: %v\nbody: %s", c.tool, err, tr.Content[0].Text)
		}
		if env.Code != 5 || !strings.Contains(env.Message, "schema_version") {
			t.Fatalf("%s: want code 5 naming schema_version, got %+v", c.tool, env)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s rewrote the item:\n%s", c.tool, got)
		}
	}

	// The v1 sibling is still reachable by id.
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 99, "method": "tools/call",
		"params": map[string]any{"name": "ask_show", "arguments": map[string]any{"id": "ask-aaaa"}},
	})
	if body, isErr := extractToolBody(t, resp); isErr {
		t.Fatalf("ask_show of the v1 sibling failed: %s", body)
	}
}
