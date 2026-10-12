package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// TestMCPResolveLockTimeout shows the MCP write path takes the store lock:
// with the lock held by the test, ask_resolve returns error code 5 and the
// item file is byte-identical; with it released, the same call resolves
// the item.
func TestMCPResolveLockTimeout(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	created := newItem(t, root, 1)
	path := filepath.Join(root, ".ask", "items", created.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	orig := core.LockTimeout
	core.LockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { core.LockTimeout = orig })
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	held, err := store.Lock()
	if err != nil {
		t.Fatalf("take lock: %v", err)
	}

	tr := callMutate(t, root, "ask_resolve", created.ID, 2)
	if !tr.IsError {
		t.Fatalf("ask_resolve with the lock held: isError=false, content %+v", tr.Content)
	}
	if len(tr.Content) != 1 {
		t.Fatalf("want 1 content part, got %d: %+v", len(tr.Content), tr.Content)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, tr.Content[0].Text)
	}
	if env.Code != 5 {
		t.Fatalf("error code %d, want 5 (message %q)", env.Code, env.Message)
	}
	want := "ask resolve: store is locked by another ask process (waited 100ms); nothing was written"
	if env.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", env.Message, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("item file changed under a timed-out ask_resolve:\nbefore %s\nafter  %s", before, after)
	}

	// Released, the same call goes through.
	held.Unlock()
	tr = callMutate(t, root, "ask_resolve", created.ID, 3)
	if tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("ask_resolve after unlock: isError=%v content %+v", tr.IsError, tr.Content)
	}
	var it core.Item
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &it); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if it.Status != core.StatusResolved {
		t.Fatalf("status %s after unlock, want resolved", it.Status)
	}
}

// TestMCPUpdateLockTimeout is the same check for ask_update, the other MCP
// tool that rewrites an existing item.
func TestMCPUpdateLockTimeout(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	created := newItem(t, root, 1)
	path := filepath.Join(root, ".ask", "items", created.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	orig := core.LockTimeout
	core.LockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { core.LockTimeout = orig })
	store, err := core.OpenStore(root, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	held, err := store.Lock()
	if err != nil {
		t.Fatalf("take lock: %v", err)
	}
	defer held.Unlock()

	tr := extractToolResult(t, driveRequest(t, root, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_update",
			"arguments": map[string]any{"id": created.ID, "body_append": "a note"},
		},
	}))
	if !tr.IsError || len(tr.Content) != 1 {
		t.Fatalf("ask_update with the lock held: isError=%v content %+v", tr.IsError, tr.Content)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, tr.Content[0].Text)
	}
	want := "ask update: store is locked by another ask process (waited 100ms); nothing was written"
	if env.Code != 5 || env.Message != want {
		t.Fatalf("envelope: code %d message %q\nwant code 5 message %q", env.Code, env.Message, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("item file changed under a timed-out ask_update")
	}
}
