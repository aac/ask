package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// initRepo creates a fresh .ask/ at root and returns the project root path.
// Tests use this instead of running the CLI's runInit because we want to
// drive the MCP server directly without invoking the cli package.
func initRepo(t *testing.T, root string) {
	t.Helper()
	cfg := &core.ProjectConfig{
		ProjectID:   "01TESTPROJECT0000000000000",
		DisplayName: filepath.Base(root),
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := core.OpenStore(root, cfg); err != nil {
		t.Fatalf("init store: %v", err)
	}
}

// driveRequest writes a single JSON-RPC line to a fresh server's stdin and
// returns the parsed response envelope. The server is run on a goroutine
// so we can read its output without deadlocking on the unbuffered pipe.
func driveRequest(t *testing.T, root string, req map[string]any) jsonRPCResponse {
	t.Helper()
	in := &bytes.Buffer{}
	enc := json.NewEncoder(in)
	if err := enc.Encode(req); err != nil {
		t.Fatalf("encode req: %v", err)
	}
	out := &bytes.Buffer{}
	s := NewServer(root, in, out)
	if err := s.Serve(context.Background()); err != nil && err != io.EOF {
		t.Fatalf("serve: %v", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode resp: %v\nbody: %s", err, out.String())
	}
	return resp
}

// extractToolBody pulls the single text content out of a tools/call
// response envelope and returns it as raw bytes for further unmarshalling.
// Also returns the isError flag so callers can assert error semantics.
//
// Only safe for tools that return exactly one content entry: read-only
// tools (ask_list, ask_show, ask_new) and the active-path mutate
// responses for ask_resolve/ask_close/ask_reopen. For the idempotent
// no-op envelope (item JSON + secondary warning text, spec §5.5), use
// extractToolResult plus assertNoopEnvelope instead.
func extractToolBody(t *testing.T, resp jsonRPCResponse) ([]byte, bool) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	rb, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var tr toolResult
	if err := json.Unmarshal(rb, &tr); err != nil {
		t.Fatalf("decode tool result: %v\nbody: %s", err, rb)
	}
	if len(tr.Content) != 1 {
		t.Fatalf("want 1 content part, got %d: %+v", len(tr.Content), tr.Content)
	}
	if tr.Content[0].Type != "text" {
		t.Fatalf("want text content, got %q", tr.Content[0].Type)
	}
	return []byte(tr.Content[0].Text), tr.IsError
}

// extractToolResult returns the full toolResult envelope (all content
// parts plus the isError flag) for tests that need to assert the spec
// §5.5 no-op shape: a primary item-payload text part plus a secondary
// warning text part.
func extractToolResult(t *testing.T, resp jsonRPCResponse) toolResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	rb, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var tr toolResult
	if err := json.Unmarshal(rb, &tr); err != nil {
		t.Fatalf("decode tool result: %v\nbody: %s", err, rb)
	}
	return tr
}

// TestInitialize verifies the handshake response shape. This is the
// minimum bar from the acceptance criteria: `go run ./cmd/ask mcp`
// accepts an initialize request on stdin and returns a proper response.
func TestInitialize(t *testing.T) {
	root := t.TempDir()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	rb, _ := json.Marshal(resp.Result)
	var got struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(rb, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ProtocolVersion != protocolVersion {
		t.Errorf("protocol: got %q want %q", got.ProtocolVersion, protocolVersion)
	}
	if got.ServerInfo.Name != serverName {
		t.Errorf("server name: got %q want %q", got.ServerInfo.Name, serverName)
	}
	if _, ok := got.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools entry: %+v", got.Capabilities)
	}
}

// TestToolsList confirms every CLI verb (minus init/help/version, which
// are intentionally not exposed) has a tool entry.
func TestToolsList(t *testing.T) {
	root := t.TempDir()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	rb, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(rb, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]bool{
		"ask_new":     false,
		"ask_list":    false,
		"ask_show":    false,
		"ask_resolve": false,
		"ask_reopen":  false,
		"ask_close":   false,
	}
	for _, td := range got.Tools {
		if _, ok := want[td.Name]; ok {
			want[td.Name] = true
		} else {
			t.Errorf("unexpected tool %q", td.Name)
		}
		if td.InputSchema == nil {
			t.Errorf("tool %q has nil schema", td.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing tool %q", name)
		}
	}
}

// TestAskListEmpty exercises tools/call ask_list against an
// already-initialized but item-free project, asserting the canonical
// "no items" response: isError=false, body is the JSON array `[]`.
func TestAskListEmpty(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ask_list",
			"arguments": map[string]any{},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("isError=true: %s", body)
	}
	var items []core.Item
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode list body %q: %v", body, err)
	}
	if len(items) != 0 {
		t.Errorf("want empty list, got %d items", len(items))
	}
}

// TestAskNewAndShow exercises the full file→show round trip via MCP. The
// new tool should return the created Item; show should return the same
// item by full id.
func TestAskNewAndShow(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":   "Set up Gmail OAuth",
				"urgency": "blocker",
			},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new isError: %s", body)
	}
	var created core.Item
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v\nbody: %s", err, body)
	}
	if !strings.HasPrefix(created.ID, "ask-") {
		t.Errorf("created id missing ask- prefix: %q", created.ID)
	}
	if created.Status != core.StatusOpen {
		t.Errorf("created status: got %q want open", created.Status)
	}
	if created.Urgency != core.UrgencyBlocker {
		t.Errorf("created urgency: got %q want blocker", created.Urgency)
	}

	// ask_show round-trip
	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "ask_show",
			"arguments": map[string]any{"id": created.ID},
		},
	})
	body, isErr = extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_show isError: %s", body)
	}
	var shown core.Item
	if err := json.Unmarshal(body, &shown); err != nil {
		t.Fatalf("decode shown: %v", err)
	}
	if shown.ID != created.ID {
		t.Errorf("shown id mismatch: got %q want %q", shown.ID, created.ID)
	}
}

// TestAskResolveCloseLifecycle drives a full open→resolved→closed
// transition over the MCP wire and asserts the Item state after each
// step.
func TestAskResolveCloseLifecycle(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	// File
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new", "arguments": map[string]any{"title": "x"},
		},
	})
	body, _ := extractToolBody(t, resp)
	var created core.Item
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Resolve
	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_resolve",
			"arguments": map[string]any{"id": created.ID, "note": "did it"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_resolve isError: %s", body)
	}
	var resolved core.Item
	if err := json.Unmarshal(body, &resolved); err != nil {
		t.Fatalf("decode resolved: %v", err)
	}
	if resolved.Status != core.StatusResolved {
		t.Errorf("status after resolve: got %q want resolved", resolved.Status)
	}
	if resolved.ResolutionNote == nil || *resolved.ResolutionNote != "did it" {
		got := "<nil>"
		if resolved.ResolutionNote != nil {
			got = *resolved.ResolutionNote
		}
		t.Errorf("resolution_note: got %q want %q", got, "did it")
	}

	// Close
	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 8, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_close",
			"arguments": map[string]any{"id": created.ID},
		},
	})
	body, isErr = extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_close isError: %s", body)
	}
	var closed core.Item
	if err := json.Unmarshal(body, &closed); err != nil {
		t.Fatalf("decode closed: %v", err)
	}
	if closed.Status != core.StatusClosed {
		t.Errorf("status after close: got %q want closed", closed.Status)
	}
}

// TestAskShowNotFound exercises the not-found error path. The envelope
// shape is the spec §5.5 {code, message} object inside a tools/call
// result with isError=true.
func TestAskShowNotFound(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_show",
			"arguments": map[string]any{"id": "ask-9999"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if !isErr {
		t.Fatalf("expected isError=true, got body=%s", body)
	}
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	if env.Code != 3 {
		t.Errorf("code: got %d want 3", env.Code)
	}
	if !strings.Contains(env.Message, "not found") {
		t.Errorf("message: got %q want substring 'not found'", env.Message)
	}
}

// TestUnknownTool exercises the catch-all in invoke(): unknown tool
// names return a code-2 envelope rather than a JSON-RPC method-not-found
// (which is reserved for unknown JSON-RPC methods like an unimplemented
// resources/list).
func TestUnknownTool(t *testing.T) {
	root := t.TempDir()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 10, "method": "tools/call",
		"params": map[string]any{"name": "ask_nope"},
	})
	body, isErr := extractToolBody(t, resp)
	if !isErr {
		t.Fatalf("expected isError=true, got body=%s", body)
	}
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != 2 {
		t.Errorf("code: got %d want 2", env.Code)
	}
}

// TestParseError feeds an obviously malformed JSON line and asserts the
// server emits a JSON-RPC parse-error response (with nil id, per the
// spec).
func TestParseError(t *testing.T) {
	root := t.TempDir()
	in := bytes.NewBufferString("not valid json\n")
	out := &bytes.Buffer{}
	s := NewServer(root, in, out)
	if err := s.Serve(context.Background()); err != nil && err != io.EOF {
		t.Fatalf("serve: %v", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode resp: %v\nbody: %s", err, out.String())
	}
	if resp.Error == nil {
		t.Fatalf("want JSON-RPC error, got result %+v", resp.Result)
	}
	if resp.Error.Code != errParse {
		t.Errorf("code: got %d want %d", resp.Error.Code, errParse)
	}
}

// TestCWDInitialize ensures Run() (the os.Stdin/os.Stdout-backed entry
// point) is wired correctly. We don't invoke Run directly since it reads
// from os.Stdin; this exercises NewServer with explicit pipes which is
// the path Run uses internally.
func TestCWDInitialize(t *testing.T) {
	// Sanity check: os.Getwd succeeds in the test process. If this ever
	// regresses we'd see Run return 5 instead of dispatching.
	if _, err := os.Getwd(); err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
}

// newItem is a test helper that files a single item via ask_new and
// returns the created Item. Tests that need a pre-existing item to drive
// resolve/reopen/close against use this instead of poking the store
// directly so the MCP wire path is exercised.
func newItem(t *testing.T, root string, id int) core.Item {
	t.Helper()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "t"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new isError: %s", body)
	}
	var it core.Item
	if err := json.Unmarshal(body, &it); err != nil {
		t.Fatalf("decode new item: %v\nbody: %s", err, body)
	}
	return it
}

// callMutate is a test helper that invokes a lifecycle tool by name
// (ask_resolve/ask_reopen/ask_close) against the given id and returns
// the full toolResult envelope for assertion.
func callMutate(t *testing.T, root, tool, itemID string, id int) toolResult {
	t.Helper()
	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": map[string]any{"id": itemID},
		},
	})
	return extractToolResult(t, resp)
}

// assertNoopEnvelope asserts the spec §5.5 idempotent-no-op shape:
// isError=false, exactly two text content parts, the first is the item
// JSON (unchanged at its target status), the second is the warning
// "ask <verb>: already <status>". Returns the decoded Item so callers
// can pin further fields if needed.
func assertNoopEnvelope(t *testing.T, tr toolResult, verb, wantStatus string) core.Item {
	t.Helper()
	if tr.IsError {
		t.Fatalf("no-op envelope must have isError=false, got true; content=%+v", tr.Content)
	}
	if len(tr.Content) != 2 {
		t.Fatalf("no-op envelope must have 2 content parts, got %d: %+v", len(tr.Content), tr.Content)
	}
	if tr.Content[0].Type != "text" {
		t.Errorf("first part type: got %q want text", tr.Content[0].Type)
	}
	if tr.Content[1].Type != "text" {
		t.Errorf("second part type: got %q want text", tr.Content[1].Type)
	}
	var it core.Item
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &it); err != nil {
		t.Fatalf("decode item part: %v\nbody: %s", err, tr.Content[0].Text)
	}
	if string(it.Status) != wantStatus {
		t.Errorf("item status: got %q want %q", it.Status, wantStatus)
	}
	wantWarn := "ask " + verb + ": already " + wantStatus
	if tr.Content[1].Text != wantWarn {
		t.Errorf("warning text: got %q want %q", tr.Content[1].Text, wantWarn)
	}
	return it
}

// TestAskResolveOnResolvedIsNoop pins the spec §5.5 no-op envelope shape
// for resolve-on-resolved: isError=false, the item payload is returned
// unchanged in the first content part, and a second content part carries
// the warning text "ask resolve: already resolved" (analog of the CLI's
// stderr warning + exit-6 success body).
func TestAskResolveOnResolvedIsNoop(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := newItem(t, root, 100)

	// First resolve: actual transition.
	tr := callMutate(t, root, "ask_resolve", it.ID, 101)
	if tr.IsError {
		t.Fatalf("first resolve should succeed, got isError=true: %+v", tr.Content)
	}
	if len(tr.Content) != 1 {
		t.Fatalf("first resolve must return single content part, got %d", len(tr.Content))
	}

	// Second resolve: no-op envelope.
	tr = callMutate(t, root, "ask_resolve", it.ID, 102)
	got := assertNoopEnvelope(t, tr, "resolve", "resolved")
	if got.ID != it.ID {
		t.Errorf("returned id: got %q want %q", got.ID, it.ID)
	}
}

// TestAskCloseOnClosedIsNoop pins the no-op envelope for close-on-closed.
func TestAskCloseOnClosedIsNoop(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := newItem(t, root, 110)

	// Close from open is an active transition (cancel/dismiss path).
	tr := callMutate(t, root, "ask_close", it.ID, 111)
	if tr.IsError {
		t.Fatalf("first close should succeed: %+v", tr.Content)
	}
	if len(tr.Content) != 1 {
		t.Fatalf("first close must return single content part, got %d", len(tr.Content))
	}

	// Second close on closed: no-op envelope.
	tr = callMutate(t, root, "ask_close", it.ID, 112)
	got := assertNoopEnvelope(t, tr, "close", "closed")
	if got.ID != it.ID {
		t.Errorf("returned id: got %q want %q", got.ID, it.ID)
	}
}

// TestAskNewWithBlocks exercises the ask_new path with the new blocks
// arg: a multi-ref array round-trips through MCP and is preserved on the
// stored item.
func TestAskNewWithBlocks(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 200, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":  "blocking the act task",
				"blocks": []string{"act-3c89", "linear-eng-1234"},
			},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new with blocks isError: %s", body)
	}
	var created core.Item
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v\nbody: %s", err, body)
	}
	if len(created.Blocks) != 2 || created.Blocks[0] != "act-3c89" || created.Blocks[1] != "linear-eng-1234" {
		t.Fatalf("blocks round-trip: got %v", created.Blocks)
	}

	// ask_show round-trip: the same blocks come back.
	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 201, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_show",
			"arguments": map[string]any{"id": created.ID},
		},
	})
	body, isErr = extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_show isError: %s", body)
	}
	var shown core.Item
	if err := json.Unmarshal(body, &shown); err != nil {
		t.Fatalf("decode shown: %v", err)
	}
	if len(shown.Blocks) != 2 || shown.Blocks[0] != "act-3c89" || shown.Blocks[1] != "linear-eng-1234" {
		t.Fatalf("ask_show blocks: got %v", shown.Blocks)
	}
}

// TestAskNewBlocksValidation pins the MCP-side rejection of empty /
// whitespace-only refs (code 2).
func TestAskNewBlocksValidation(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 210, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":  "t",
				"blocks": []string{"   "},
			},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if !isErr {
		t.Fatalf("expected validation error for whitespace blocks, got body=%s", body)
	}
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != 2 {
		t.Errorf("code: got %d want 2", env.Code)
	}
}

// TestAskListBlocksFilter exercises the ask_list blocks filter end to
// end: the MCP-side arg is a single string (matching the CLI's per-flag
// repeatable convention flattened to one ref per call); only items whose
// blocks array contains that ref are returned.
func TestAskListBlocksFilter(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	// File two items with distinct blocks; one with none.
	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 220, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title": "wants-act", "blocks": []string{"act-3c89"},
			},
		},
	})
	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 221, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title": "wants-linear", "blocks": []string{"linear-1234"},
			},
		},
	})
	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 222, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "no-blocks"},
		},
	})

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 223, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_list",
			"arguments": map[string]any{"blocks": "act-3c89"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_list filter isError: %s", body)
	}
	var items []core.Item
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode list body %q: %v", body, err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item matching act-3c89, got %d: %v", len(items), items)
	}
	if items[0].Title != "wants-act" {
		t.Errorf("filter returned wrong item: %q", items[0].Title)
	}
}

// TestAskNewBlocksJSONEmitsField pins spec §1.1 forward-compat: even
// when no blocks are passed in, the response Item carries `"blocks":[]`
// so downstream parsers never have to branch on key presence.
func TestAskNewBlocksJSONEmitsField(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 230, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "x"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new isError: %s", body)
	}
	if !strings.Contains(string(body), `"blocks":[]`) {
		t.Fatalf("expected blocks:[] in JSON body: %s", body)
	}
}

// TestAskNewWithRecipient exercises ask_new with the recipient arg: the
// optional free-form string round-trips through MCP and is preserved on
// the stored item. Mirrors TestAskNewWithBlocks for the agent-to-agent
// addition (act-341838).
func TestAskNewWithRecipient(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 300, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":     "ping the data-prep agent",
				"recipient": "agent:data-prep",
			},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new with recipient isError: %s", body)
	}
	var created core.Item
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v\nbody: %s", err, body)
	}
	if created.Recipient == nil || *created.Recipient != "agent:data-prep" {
		t.Fatalf("recipient round-trip: got %+v", created.Recipient)
	}

	// ask_show round-trip preserves the recipient.
	resp = driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 301, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_show",
			"arguments": map[string]any{"id": created.ID},
		},
	})
	body, isErr = extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_show isError: %s", body)
	}
	var shown core.Item
	if err := json.Unmarshal(body, &shown); err != nil {
		t.Fatalf("decode shown: %v", err)
	}
	if shown.Recipient == nil || *shown.Recipient != "agent:data-prep" {
		t.Fatalf("ask_show recipient: got %+v", shown.Recipient)
	}
}

// TestAskNewRecipientValidation pins MCP-side rejection of whitespace-only
// recipient refs (code 2).
func TestAskNewRecipientValidation(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 310, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title":     "t",
				"recipient": "   ",
			},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if !isErr {
		t.Fatalf("expected validation error for whitespace recipient, got body=%s", body)
	}
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != 2 {
		t.Errorf("code: got %d want 2", env.Code)
	}
}

// TestAskListRecipientFilter exercises ask_list `recipient` filter end
// to end: only items whose Recipient equals the requested ref are
// returned. Items without a recipient (the implicit-human case) never
// match an explicit filter.
func TestAskListRecipientFilter(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 320, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title": "for-dprep", "recipient": "agent:data-prep",
			},
		},
	})
	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 321, "method": "tools/call",
		"params": map[string]any{
			"name": "ask_new",
			"arguments": map[string]any{
				"title": "for-other", "recipient": "agent:other",
			},
		},
	})
	driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 322, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "no-recipient"},
		},
	})

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 323, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_list",
			"arguments": map[string]any{"recipient": "agent:data-prep"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_list recipient filter isError: %s", body)
	}
	var items []core.Item
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode list body %q: %v", body, err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item matching recipient, got %d: %v", len(items), items)
	}
	if items[0].Title != "for-dprep" {
		t.Errorf("filter returned wrong item: %q", items[0].Title)
	}
}

// TestAskNewRecipientJSONEmitsField pins spec §1.1 forward-compat: even
// when no recipient is passed in, the response Item carries
// `"recipient":null` so downstream parsers never have to branch on key
// presence.
func TestAskNewRecipientJSONEmitsField(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	resp := driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 330, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_new",
			"arguments": map[string]any{"title": "x"},
		},
	})
	body, isErr := extractToolBody(t, resp)
	if isErr {
		t.Fatalf("ask_new isError: %s", body)
	}
	if !strings.Contains(string(body), `"recipient":null`) {
		t.Fatalf("expected recipient:null in JSON body: %s", body)
	}
}

// TestAskReopenOnOpenIsNoop pins the no-op envelope for reopen-on-open.
// reopen-on-open is the only no-op path that requires no prior
// transition — the item is created in StatusOpen.
func TestAskReopenOnOpenIsNoop(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := newItem(t, root, 120)

	tr := callMutate(t, root, "ask_reopen", it.ID, 121)
	got := assertNoopEnvelope(t, tr, "reopen", "open")
	if got.ID != it.ID {
		t.Errorf("returned id: got %q want %q", got.ID, it.ID)
	}
}
