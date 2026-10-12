package mcp

import (
	"bytes"
	"context"
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

// mcpdriftRawResult sends one tools/call and returns the `result` object
// as the keys actually on the wire. The typed toolResult cannot tell an
// absent isError from `"isError": false`; this can.
func mcpdriftRawResult(t *testing.T, root, tool string, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	in := &bytes.Buffer{}
	if err := json.NewEncoder(in).Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	}); err != nil {
		t.Fatalf("encode req: %v", err)
	}
	out := &bytes.Buffer{}
	if err := NewServer(root, in, out).Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var env struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v\nbody: %.300s", err, out.String())
	}
	if env.Result == nil {
		t.Fatalf("no result object on the wire: %.300s", out.String())
	}
	return env.Result
}

// TestIsErrorOmittedOnSuccess pins the wire shape spec §5.1 documents: a
// successful result has no isError key (absent means false, per MCP), and
// an error result carries `"isError": true`. Covers a plain success, the
// two-part no-op envelope, and an error.
func TestIsErrorOmittedOnSuccess(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "", 1)

	successes := []struct {
		name, tool string
		args       map[string]any
	}{
		{"ask_new", "ask_new", map[string]any{"title": "another"}},
		{"ask_list", "ask_list", map[string]any{}},
		{"ask_show", "ask_show", map[string]any{"id": it.ID}},
		{"ask_update", "ask_update", map[string]any{"id": it.ID, "body": "b"}},
		{"ask_reopen no-op", "ask_reopen", map[string]any{"id": it.ID}},
		{"ask_resolve", "ask_resolve", map[string]any{"id": it.ID}},
	}
	for _, tc := range successes {
		t.Run(tc.name, func(t *testing.T) {
			res := mcpdriftRawResult(t, root, tc.tool, tc.args)
			if raw, present := res["isError"]; present {
				t.Fatalf("success result carries isError=%s; the key must be absent", raw)
			}
			if _, ok := res["content"]; !ok {
				t.Fatalf("success result has no content: %v", res)
			}
		})
	}

	t.Run("error carries isError true", func(t *testing.T) {
		res := mcpdriftRawResult(t, root, "ask_show", map[string]any{"id": "ask-ffff"})
		if string(res["isError"]) != "true" {
			t.Fatalf("error result isError: got %q want true", res["isError"])
		}
	})
}

// mcpdriftErrCode returns the code of a single-part error envelope.
func mcpdriftErrCode(t *testing.T, tr toolResult) int {
	t.Helper()
	if !tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("want a single-part error, got isError=%v with %d part(s)", tr.IsError, len(tr.Content))
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %.200s", err, tr.Content[0].Text)
	}
	return env.Code
}

// TestErrorCodesReachableOverMCP drives each code spec §5.5 and
// `ask help mcp` list — 2, 3, 4, 5, 7 — out of the real server, so the
// documented list is checked against the wire rather than against itself.
func TestErrorCodesReachableOverMCP(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	// Two ids sharing the "ask-" stem make the bare prefix ambiguous once
	// both exist; file a few so at least two share a first hex digit.
	var ids []string
	for i := 0; i < 17; i++ {
		ids = append(ids, mcpupdNew(t, root, "item "+strings.Repeat("i", i+1), "", i+1).ID)
	}
	ambiguous := ""
	seen := map[string]bool{}
	for _, id := range ids {
		p := id[:len("ask-")+1]
		if seen[p] {
			ambiguous = p
			break
		}
		seen[p] = true
	}
	if ambiguous == "" {
		t.Fatalf("17 ids with no shared first hex digit: %v", ids)
	}

	if got := mcpdriftErrCode(t, mcpdriftCall(t, root, "ask_new", map[string]any{"title": ""})); got != 2 {
		t.Errorf("validation error: code %d want 2", got)
	}
	if got := mcpdriftErrCode(t, mcpdriftCall(t, root, "ask_show", map[string]any{"id": "ask-zzzz"})); got != 3 {
		t.Errorf("unknown id: code %d want 3", got)
	}
	if got := mcpdriftErrCode(t, mcpdriftCall(t, root, "ask_show", map[string]any{"id": ambiguous})); got != 4 {
		t.Errorf("ambiguous prefix %q: code %d want 4", ambiguous, got)
	}
	if got := mcpdriftErrCode(t, mcpdriftCall(t, t.TempDir(), "ask_list", map[string]any{})); got != 5 {
		t.Errorf("no store: code %d want 5", got)
	}

	// Stranded: the items stay, config.json goes. Every tool reports 7.
	if err := os.Remove(filepath.Join(root, ".ask", "config.json")); err != nil {
		t.Fatalf("remove config.json: %v", err)
	}
	for _, td := range tools() {
		args := mcpdriftBaseArgs(td.Name, ids[0])
		if td.Name == "ask_update" {
			args["title"] = "x"
		}
		if got := mcpdriftErrCode(t, mcpdriftCall(t, root, td.Name, args)); got != 7 {
			t.Errorf("%s on a stranded store: code %d want 7", td.Name, got)
		}
	}
}

// TestNoopIsNeverCodeSix: the CLI's exit 6 has no MCP error code. Every
// idempotent no-op is a success whose first part is the Item, not a
// {code, message} envelope.
func TestNoopIsNeverCodeSix(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "same", 1)
	noops := []struct {
		tool string
		args map[string]any
	}{
		{"ask_reopen", map[string]any{"id": it.ID}},
		{"ask_update", map[string]any{"id": it.ID, "body": "same"}},
	}
	for _, tc := range noops {
		tr := mcpdriftCall(t, root, tc.tool, tc.args)
		if tr.IsError || len(tr.Content) != 2 {
			t.Fatalf("%s no-op: want success with 2 parts, got isError=%v parts=%d", tc.tool, tr.IsError, len(tr.Content))
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &probe); err != nil {
			t.Fatalf("%s no-op: decode first part: %v", tc.tool, err)
		}
		if _, has := probe["code"]; has {
			t.Errorf("%s no-op: first part is an error envelope (%s), want the Item", tc.tool, tr.Content[0].Text)
		}
		if _, has := probe["id"]; !has {
			t.Errorf("%s no-op: first part is not an Item: %.120s", tc.tool, tr.Content[0].Text)
		}
	}
}

// TestAskNewTitleMessagesUnchanged pins ask_new's title refusals word for
// word, so routing them through core.ValidateTitle changes nothing a
// caller can see.
func TestAskNewTitleMessagesUnchanged(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	cases := []struct{ name, title, want string }{
		{"empty", "", "ask new: title is required"},
		{"whitespace", "  \t ", "ask new: title is required"},
		{"too long", strings.Repeat("x", 201), "ask new: title must be 1..200 characters"},
		{"newline", "a\nb", "ask new: title must not contain newlines"},
		{"carriage return", "a\rb", "ask new: title must not contain newlines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := mcpdriftCall(t, root, "ask_new", map[string]any{"title": tc.title})
			if got := mcpdriftErrCode(t, tr); got != 2 {
				t.Errorf("code %d want 2", got)
			}
			var env errEnvelope
			_ = json.Unmarshal([]byte(tr.Content[0].Text), &env)
			if env.Message != tc.want {
				t.Errorf("message: got %q want %q", env.Message, tc.want)
			}
		})
	}
	// Surrounding whitespace is trimmed and 200 characters is accepted.
	tr := mcpdriftCall(t, root, "ask_new", map[string]any{"title": " " + strings.Repeat("x", 200) + " "})
	if tr.IsError {
		t.Fatalf("a 200-character title was refused: %.200s", tr.Content[0].Text)
	}
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

// TestBodyAppendCapsTheArgumentNotTheResult: each body_append is capped
// at 16384 characters; the body it produces is not held to that cap, only
// to the far higher maxAppendedBodyLen ceiling (act-58c1b4). The
// caps are limits on what one call may send; the CLI files bodies of any
// length, so a stored body may be longer and appends to it must land.
func TestBodyAppendCapsTheArgumentNotTheResult(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	// An append costs the two-character blank-line separator plus the
	// text: 16381 + 2 + 1 lands exactly on 16384.
	it := mcpupdNew(t, root, "seed", strings.Repeat("x", 16381), 1)

	tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "y"})
	if tr.IsError {
		t.Fatalf("append landing exactly on 16384 was refused: %.200s", tr.Content[0].Text)
	}
	if got := mcpupdShow(t, root, it.ID, 2).Body; len(got) != 16384 {
		t.Fatalf("body length after append: got %d want 16384", len(got))
	}

	// One more takes the stored body past 16384, and is accepted.
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body_append": "z"})
	if tr.IsError {
		t.Fatalf("append taking the stored body past 16384 was refused: %.200s", tr.Content[0].Text)
	}
	if got := mcpupdShow(t, root, it.ID, 3).Body; len(got) != 16387 {
		t.Fatalf("body length after second append: got %d want 16387", len(got))
	}

	// A body replacement is an argument, so it is still capped.
	before := mcpdriftSnapshot(t, root)
	tr = mcpdriftCall(t, root, "ask_update", map[string]any{"id": it.ID, "body": strings.Repeat("x", 16385)})
	mcpdriftWantTooLong(t, tr, "body", 16384)
	mcpdriftSameStore(t, root, before)
}
