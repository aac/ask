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

// listskipSeed creates a store of open normal items with the given ids.
func listskipSeed(t *testing.T, root string, ids ...string) {
	t.Helper()
	initRepo(t, root)
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := store.Save(core.NewItem(id, "probe", core.UrgencyNormal, core.StatusOpen, time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
}

// listskipV2 hand-sets id's file to schema_version "2"; listskipCorrupt
// truncates it to non-JSON. Both return the bytes now on disk.
func listskipV2(t *testing.T, root, id string) []byte {
	t.Helper()
	p := filepath.Join(root, ".ask", "items", id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	const from = `"schema_version": "1",`
	if !bytes.Contains(b, []byte(from)) {
		t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
	}
	b = bytes.Replace(b, []byte(from), []byte(`"schema_version": "2",`), 1)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func listskipCorrupt(t *testing.T, root, id string) []byte {
	t.Helper()
	b := []byte(`{"id": "` + id)
	if err := os.WriteFile(filepath.Join(root, ".ask", "items", id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func listskipCall(t *testing.T, root string, args map[string]any) toolResult {
	t.Helper()
	return extractToolResult(t, driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ask_list", "arguments": args},
	}))
}

// listskipItems decodes content[0] as the item array and returns its ids.
func listskipItems(t *testing.T, tr toolResult) []string {
	t.Helper()
	if tr.IsError || len(tr.Content) == 0 {
		t.Fatalf("want a success result with content, got %+v", tr)
	}
	var items []core.Item
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &items); err != nil {
		t.Fatalf("content[0] is not an item array: %v\n%s", err, tr.Content[0].Text)
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func listskipSameBytes(t *testing.T, root, id string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, ".ask", "items", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s was rewritten:\n%s", id, got)
	}
}

// TestListskipMCPAcceptance is act-d1ad86's MCP acceptance: two items, one
// hand-set to schema_version 2. ask_list returns the readable item and
// surfaces the skipped id — isError=false, the array in content[0], and a
// second text part carrying the CLI's stderr line.
func TestListskipMCPAcceptance(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb")
	want := listskipV2(t, root, "ask-bbbb")

	tr := listskipCall(t, root, map[string]any{})
	if ids := listskipItems(t, tr); len(ids) != 1 || ids[0] != "ask-aaaa" {
		t.Fatalf("content[0]: want [ask-aaaa], got %v", ids)
	}
	if len(tr.Content) != 2 || tr.Content[1].Type != "text" {
		t.Fatalf("want the array plus one warning part, got %+v", tr.Content)
	}
	w := tr.Content[1].Text
	if !strings.HasPrefix(w, "ask list: skipped ask-bbbb: ") || !strings.Contains(w, "schema_version") {
		t.Fatalf("warning part must name the skipped id and schema_version, got %q", w)
	}
	listskipSameBytes(t, root, "ask-bbbb", want)
}

// TestListskipMCPMixed: a corrupt file and a schema_version "2" file next
// to a readable item give one warning part per skipped id, in id order,
// after the array — with and without filters.
func TestListskipMCPMixed(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb", "ask-cccc")
	wantV2 := listskipV2(t, root, "ask-cccc")
	wantCorrupt := listskipCorrupt(t, root, "ask-bbbb")

	for _, args := range []map[string]any{{}, {"all": true}, {"status": []string{"open"}}} {
		tr := listskipCall(t, root, args)
		if ids := listskipItems(t, tr); len(ids) != 1 || ids[0] != "ask-aaaa" {
			t.Fatalf("%v: content[0]: want [ask-aaaa], got %v", args, ids)
		}
		if len(tr.Content) != 3 ||
			!strings.HasPrefix(tr.Content[1].Text, "ask list: skipped ask-bbbb: ") || !strings.Contains(tr.Content[1].Text, "corrupt") ||
			!strings.HasPrefix(tr.Content[2].Text, "ask list: skipped ask-cccc: ") || !strings.Contains(tr.Content[2].Text, "schema_version") {
			t.Fatalf("%v: want one warning part per skipped id in id order, got %+v", args, tr.Content)
		}
	}

	// A filter that excludes the one readable item is still a success:
	// an empty array, and the skips still reported.
	tr := listskipCall(t, root, map[string]any{"status": []string{"closed"}})
	if ids := listskipItems(t, tr); len(ids) != 0 || tr.Content[0].Text != "[]" || len(tr.Content) != 3 {
		t.Fatalf("filtered-out: want [] plus two warnings, got %+v", tr.Content)
	}
	listskipSameBytes(t, root, "ask-bbbb", wantCorrupt)
	listskipSameBytes(t, root, "ask-cccc", wantV2)
}

// TestListskipMCPAllUnreadable: when no item at all is readable the call
// is an error — one content part, code 5, the message naming every
// skipped id.
func TestListskipMCPAllUnreadable(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb")
	listskipCorrupt(t, root, "ask-aaaa")
	listskipV2(t, root, "ask-bbbb")

	tr := listskipCall(t, root, map[string]any{})
	if !tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("want isError with exactly one content part, got %+v", tr)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, tr.Content[0].Text)
	}
	if env.Code != 5 {
		t.Fatalf("want code 5, got %+v", env)
	}
	for _, want := range []string{"no readable items (2 unreadable)", "skipped ask-aaaa: ", "skipped ask-bbbb: ", "schema_version"} {
		if !strings.Contains(env.Message, want) {
			t.Fatalf("message missing %q: %q", want, env.Message)
		}
	}
}

// TestListskipMCPCleanStoresKeepOneContentPart: an empty store and a store
// with only readable items return exactly one content part, so a client
// that has never heard of skips sees the shape it always did.
func TestListskipMCPCleanStoresKeepOneContentPart(t *testing.T) {
	empty := t.TempDir()
	initRepo(t, empty)
	tr := listskipCall(t, empty, map[string]any{})
	if tr.IsError || len(tr.Content) != 1 || tr.Content[0].Text != "[]" {
		t.Fatalf("empty store: want a single [] part, got %+v", tr)
	}

	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa")
	tr = listskipCall(t, root, map[string]any{})
	if ids := listskipItems(t, tr); len(ids) != 1 || len(tr.Content) != 1 {
		t.Fatalf("clean store: want one item and one content part, got %+v", tr)
	}
}

// TestListskipMCPUnrelatedIDsStayReachable: a bad file next to a good item
// does not stop MCP tools addressing the good one, by id or by prefix.
func TestListskipMCPUnrelatedIDsStayReachable(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb", "ask-cccc")
	listskipV2(t, root, "ask-bbbb")
	listskipCorrupt(t, root, "ask-cccc")

	for i, c := range []struct {
		tool string
		args map[string]any
	}{
		{"ask_show", map[string]any{"id": "ask-aaaa"}},
		{"ask_show", map[string]any{"id": "aaa"}},
		{"ask_update", map[string]any{"id": "ask-aaaa", "body": "edited"}},
		{"ask_resolve", map[string]any{"id": "ask-aaaa"}},
		{"ask_new", map[string]any{"title": "filed next to bad files"}},
	} {
		tr := extractToolResult(t, driveRequest(t, root, map[string]any{
			"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]any{"name": c.tool, "arguments": c.args},
		}))
		if tr.IsError {
			t.Fatalf("%s %v: unexpected error %+v", c.tool, c.args, tr.Content)
		}
	}
}
