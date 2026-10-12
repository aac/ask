package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the places the MCP wire and its written contract had drifted
// apart: the schemas advertised maxLength caps the handlers did not
// enforce.

// mcpdriftCall invokes one tool and returns the full result envelope.
func mcpdriftCall(t *testing.T, root, tool string, args map[string]any) toolResult {
	t.Helper()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	return extractToolResult(t, resp)
}

// mcpdriftSnapshot reads every item file in the store, keyed by file
// name. Two equal snapshots mean no item was added, removed, or changed
// by so much as a byte.
func mcpdriftSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, ".ask", "items")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read items dir: %v", err)
	}
	snap := make(map[string]string, len(entries))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		snap[e.Name()] = string(b)
	}
	return snap
}

// mcpdriftSameStore fails unless the store reads exactly as it did in
// before.
func mcpdriftSameStore(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := mcpdriftSnapshot(t, root)
	if len(after) != len(before) {
		t.Fatalf("store changed: %d item file(s) before, %d after", len(before), len(after))
	}
	for name, want := range before {
		got, ok := after[name]
		if !ok {
			t.Fatalf("store changed: %s is gone", name)
		}
		if got != want {
			t.Fatalf("store changed: %s is not byte-identical (%d bytes before, %d after)", name, len(want), len(got))
		}
	}
}

// mcpdriftWantTooLong asserts a code-2 error envelope whose message names
// the field and the limit.
func mcpdriftWantTooLong(t *testing.T, tr toolResult, field string, limit int) {
	t.Helper()
	if !tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("want a single-part error, got isError=%v with %d part(s)", tr.IsError, len(tr.Content))
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %.200s", err, tr.Content[0].Text)
	}
	if env.Code != 2 {
		t.Errorf("code: got %d want 2 (message %q)", env.Code, env.Message)
	}
	limitStr, _ := json.Marshal(limit)
	if !strings.Contains(env.Message, field) || !strings.Contains(env.Message, string(limitStr)) {
		t.Errorf("message %q must name the field %q and the limit %d", env.Message, field, limit)
	}
}

// mcpdriftBaseArgs returns the smallest valid argument set for a tool, so
// a test can add one field to it. itemID is an existing item.
func mcpdriftBaseArgs(tool, itemID string) map[string]any {
	switch tool {
	case "ask_new":
		return map[string]any{"title": "t"}
	case "ask_list":
		return map[string]any{}
	default:
		return map[string]any{"id": itemID}
	}
}

// TestMaxLengthAdvertisedIsEnforced walks the schemas tools/list actually
// advertises and, for every top-level string property carrying a
// maxLength, sends that many characters (accepted) and one more
// (refused, code 2, store untouched). Reading the caps off the real
// schemas means a cap added to a schema later is covered here without
// anyone remembering to add a case.
func TestMaxLengthAdvertisedIsEnforced(t *testing.T) {
	checked := 0
	for _, td := range tools() {
		props, _ := td.InputSchema["properties"].(map[string]any)
		for field, raw := range props {
			prop, _ := raw.(map[string]any)
			limit, ok := prop["maxLength"].(int)
			if !ok || prop["type"] != "string" {
				continue
			}
			checked++
			t.Run(td.Name+"/"+field, func(t *testing.T) {
				for _, over := range []bool{false, true} {
					root := t.TempDir()
					initRepo(t, root)
					it := mcpupdNew(t, root, "seed", "", 1)
					before := mcpdriftSnapshot(t, root)

					n := limit
					if over {
						n++
					}
					args := mcpdriftBaseArgs(td.Name, it.ID)
					args[field] = strings.Repeat("x", n)
					tr := mcpdriftCall(t, root, td.Name, args)

					if !over {
						if tr.IsError {
							t.Fatalf("%d characters (the advertised limit) was refused: %.200s", n, tr.Content[0].Text)
						}
						continue
					}
					if !tr.IsError {
						t.Fatalf("%d characters (limit %d) was accepted", n, limit)
					}
					var env errEnvelope
					if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
						t.Fatalf("decode error envelope: %v", err)
					}
					if env.Code != 2 {
						t.Errorf("code: got %d want 2 (message %q)", env.Code, env.Message)
					}
					mcpdriftSameStore(t, root, before)
				}
			})
		}
	}
	// Guard the walk itself: if the schema shape changes so that nothing
	// matches, this test must not pass by checking nothing.
	if checked < 12 {
		t.Fatalf("walked only %d capped string properties; the schema walk is no longer finding them", checked)
	}
}

// TestMaxLengthMessageNamesFieldAndLimit pins the refusal text for the
// caps that had no enforcement before: field name and limit both appear.
func TestMaxLengthMessageNamesFieldAndLimit(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "", 1)
	before := mcpdriftSnapshot(t, root)
	long := func(n int) string { return strings.Repeat("x", n+1) }

	cases := []struct {
		tool  string
		field string
		limit int
		args  map[string]any
	}{
		{"ask_new", "body", 16384, map[string]any{"title": "t", "body": long(16384)}},
		{"ask_new", "filed_by", 256, map[string]any{"title": "t", "filed_by": long(256)}},
		{"ask_new", "tracker_ref", 256, map[string]any{"title": "t", "tracker_ref": long(256)}},
		{"ask_new", "verifier.command", 4096, map[string]any{"title": "t",
			"verifier": map[string]any{"command": long(4096)}}},
		{"ask_new", "links[1].label", 120, map[string]any{"title": "t", "links": []map[string]any{
			{"label": "ok", "url": "u"}, {"label": long(120), "url": "u"}}}},
		{"ask_new", "links[0].url", 2048, map[string]any{"title": "t", "links": []map[string]any{
			{"label": "ok", "url": long(2048)}}}},
		{"ask_update", "body", 16384, map[string]any{"id": it.ID, "body": long(16384)}},
		{"ask_update", "body_append", 16384, map[string]any{"id": it.ID, "body_append": long(16384)}},
		{"ask_resolve", "note", 16384, map[string]any{"id": it.ID, "note": long(16384)}},
		{"ask_reopen", "reason", 16384, map[string]any{"id": it.ID, "reason": long(16384)}},
		{"ask_close", "reason", 16384, map[string]any{"id": it.ID, "reason": long(16384)}},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			mcpdriftWantTooLong(t, mcpdriftCall(t, root, tc.tool, tc.args), tc.field, tc.limit)
			mcpdriftSameStore(t, root, before)
		})
	}
}

// TestMaxLengthNestedAtLimitAccepted: the nested caps the schema walk
// does not reach (verifier.command, links[].label, links[].url) accept
// exactly their limit.
func TestMaxLengthNestedAtLimitAccepted(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	tr := mcpdriftCall(t, root, "ask_new", map[string]any{
		"title":    "t",
		"verifier": map[string]any{"command": strings.Repeat("x", 4096)},
		"links": []map[string]any{
			{"label": strings.Repeat("x", 120), "url": strings.Repeat("x", 2048)},
		},
	})
	if tr.IsError {
		t.Fatalf("at-limit nested fields were refused: %.200s", tr.Content[0].Text)
	}
}

// TestMaxLengthCountsCharactersNotBytes: JSON Schema's maxLength counts
// characters, so 16384 two-byte characters is within the limit and one
// more is not.
func TestMaxLengthCountsCharactersNotBytes(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	if tr := mcpdriftCall(t, root, "ask_new", map[string]any{
		"title": "t", "body": strings.Repeat("é", 16384),
	}); tr.IsError {
		t.Fatalf("16384 two-byte characters was refused: %.200s", tr.Content[0].Text)
	}
	before := mcpdriftSnapshot(t, root)
	tr := mcpdriftCall(t, root, "ask_new", map[string]any{
		"title": "t", "body": strings.Repeat("é", 16385),
	})
	mcpdriftWantTooLong(t, tr, "body", 16384)
	mcpdriftSameStore(t, root, before)
}

// TestBodyAppendBoundsResultingBody: each append is within the per-call
// cap, but the body it would produce is bounded too, so an item cannot
// be grown without limit one small append at a time.
func TestBodyAppendBoundsResultingBody(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	// An append costs the two-character blank-line separator plus the
	// text: 16381 + 2 + 1 lands exactly on the limit.
	it := mcpupdNew(t, root, "seed", strings.Repeat("x", 16381), 1)

	tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "y"})
	if tr.IsError {
		t.Fatalf("append landing exactly on the limit was refused: %.200s", tr.Content[0].Text)
	}
	if got := mcpupdShow(t, root, it.ID, 2).Body; len(got) != 16384 {
		t.Fatalf("body length after at-limit append: got %d want 16384", len(got))
	}

	before := mcpdriftSnapshot(t, root)
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "z"})
	mcpdriftWantTooLong(t, tr, "body", 16384)
	mcpdriftSameStore(t, root, before)

	// A title-only correction of an item whose body is at the limit is
	// still allowed: the bound applies to body edits.
	if tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "title": "renamed"}); tr.IsError {
		t.Fatalf("title-only update of an at-limit item was refused: %.200s", tr.Content[0].Text)
	}
}
