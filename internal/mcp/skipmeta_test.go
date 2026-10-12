package mcp

import (
	"encoding/json"
	"testing"
)

// skipmetaEntry is one element of the result's `_meta["ask/skipped"]`
// array (spec §7.2), declared here rather than borrowed from the server so
// the test pins the wire names.
type skipmetaEntry struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// skipmetaResult runs ask_list and returns the tools/call result as its
// raw top-level keys, so a test can tell an absent `_meta` from an empty
// one.
func skipmetaResult(t *testing.T, root string, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ask_list", "arguments": args},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	rb, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(rb, &keys); err != nil {
		t.Fatalf("result is not an object: %v\n%s", err, rb)
	}
	return keys
}

// skipmetaSkipped decodes `_meta["ask/skipped"]` from a raw result.
func skipmetaSkipped(t *testing.T, res map[string]json.RawMessage) []skipmetaEntry {
	t.Helper()
	raw, ok := res["_meta"]
	if !ok {
		t.Fatalf("result has no _meta key; keys: %v", skipmetaKeys(res))
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("_meta is not an object: %v\n%s", err, raw)
	}
	var out []skipmetaEntry
	if err := json.Unmarshal(meta["ask/skipped"], &out); err != nil {
		t.Fatalf(`_meta["ask/skipped"] is not an array of {id, reason}: %v\n%s`, err, raw)
	}
	return out
}

func skipmetaKeys(res map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(res))
	for k := range res {
		keys = append(keys, k)
	}
	return keys
}

// TestSkipmetaPartialListIsStructured is act-cc151c's acceptance: one
// readable item and one schema_version "2" item, and the skipped id is
// read from the result's `_meta` without parsing the prose warning. The
// parts a client already reads (content[0], the prose entry, no isError)
// are unchanged.
func TestSkipmetaPartialListIsStructured(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb")
	listskipV2(t, root, "ask-bbbb")

	res := skipmetaResult(t, root, map[string]any{})
	skipped := skipmetaSkipped(t, res)
	if len(skipped) != 1 || skipped[0].ID != "ask-bbbb" {
		t.Fatalf(`_meta["ask/skipped"]: want exactly ask-bbbb, got %+v`, skipped)
	}
	if _, ok := res["isError"]; ok {
		t.Fatalf("a partial list is a success: isError must stay absent, got %s", res["isError"])
	}
	tr := listskipCall(t, root, map[string]any{})
	if ids := listskipItems(t, tr); len(ids) != 1 || ids[0] != "ask-aaaa" || len(tr.Content) != 2 {
		t.Fatalf("content must be unchanged (the array, then one prose line), got %+v", tr.Content)
	}
	// The reason is the prose line's own reason, not a second wording.
	if want := "ask list: skipped ask-bbbb: " + skipped[0].Reason; skipped[0].Reason == "" || tr.Content[1].Text != want {
		t.Fatalf("reason must be the prose line's reason:\n prose %q\n built %q", tr.Content[1].Text, want)
	}
}

// TestSkipmetaFollowsEverySkip: one entry per skipped id, in id order, and
// still present when the filters exclude every readable item — the case
// where content[0] is `[]` and the skip is the whole story.
func TestSkipmetaFollowsEverySkip(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa", "ask-bbbb", "ask-cccc")
	listskipV2(t, root, "ask-cccc")
	listskipCorrupt(t, root, "ask-bbbb")

	for _, args := range []map[string]any{{}, {"status": []string{"closed"}}} {
		skipped := skipmetaSkipped(t, skipmetaResult(t, root, args))
		if len(skipped) != 2 || skipped[0].ID != "ask-bbbb" || skipped[1].ID != "ask-cccc" {
			t.Fatalf("%v: want ask-bbbb then ask-cccc, got %+v", args, skipped)
		}
	}
}

// TestSkipmetaAbsentWhenNothingSkipped: a clean store's result carries no
// `_meta` key at all, so its wire form is what it was before the key
// existed. The all-unreadable error envelope does not carry it either.
func TestSkipmetaAbsentWhenNothingSkipped(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa")
	res := skipmetaResult(t, root, map[string]any{})
	if len(res) != 1 || res["content"] == nil {
		t.Fatalf("clean store: want a result holding only content, got keys %v", skipmetaKeys(res))
	}

	listskipCorrupt(t, root, "ask-aaaa")
	res = skipmetaResult(t, root, map[string]any{})
	if _, ok := res["_meta"]; ok || string(res["isError"]) != "true" {
		t.Fatalf("no readable item: want the error envelope with no _meta, got keys %v", skipmetaKeys(res))
	}
}
