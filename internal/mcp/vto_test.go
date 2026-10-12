package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// vtoNew calls ask_new and returns the item it reports, failing on an
// error result.
func vtoNew(t *testing.T, root string, args map[string]any) core.Item {
	t.Helper()
	tr := mcpdriftCall(t, root, "ask_new", args)
	if tr.IsError {
		t.Fatalf("ask_new refused %v: %.300s", args, tr.Content[0].Text)
	}
	var it core.Item
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &it); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return it
}

// vtoStoredTimeout reads verifier.timeout_seconds straight from the item
// file, with no ask code between the bytes and the assertion: the read
// path maps a stored 0 to the default, so going through it could not tell
// "stored 60" from "stored 0".
func vtoStoredTimeout(t *testing.T, root, id string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".ask", "items", id+".json"))
	if err != nil {
		t.Fatalf("read item file: %v", err)
	}
	var raw struct {
		Verifier *struct {
			TimeoutSeconds *int `json:"timeout_seconds"`
		} `json:"verifier"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode item file: %v", err)
	}
	if raw.Verifier == nil || raw.Verifier.TimeoutSeconds == nil {
		t.Fatalf("item file has no verifier.timeout_seconds: %s", b)
	}
	return *raw.Verifier.TimeoutSeconds
}

// TestAskNewVerifierTimeoutDefault pins spec §1.2: a verifier supplied
// without a timeout is stored with timeout_seconds 60, an explicit one is
// stored as given. The response, the file on disk and ask_show agree.
func TestAskNewVerifierTimeoutDefault(t *testing.T) {
	cases := []struct {
		name     string
		verifier map[string]any
		want     int
	}{
		{"omitted", map[string]any{"command": "true"}, 60},
		{"explicit null", map[string]any{"command": "true", "timeout_seconds": nil}, 60},
		{"explicit", map[string]any{"command": "true", "timeout_seconds": 5}, 5},
		{"explicit maximum", map[string]any{"type": "shell", "command": "true", "timeout_seconds": 3600}, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initRepo(t, root)
			it := vtoNew(t, root, map[string]any{"title": "t", "verifier": tc.verifier})
			if it.Verifier == nil || it.Verifier.TimeoutSeconds != tc.want {
				t.Fatalf("response verifier = %+v, want timeout_seconds %d", it.Verifier, tc.want)
			}
			if got := vtoStoredTimeout(t, root, it.ID); got != tc.want {
				t.Fatalf("stored timeout_seconds = %d, want %d", got, tc.want)
			}
			shown := mcpupdShow(t, root, it.ID, 2)
			if shown.Verifier == nil || shown.Verifier.TimeoutSeconds != tc.want {
				t.Fatalf("ask_show verifier = %+v, want timeout_seconds %d", shown.Verifier, tc.want)
			}
		})
	}
}

// TestExplicitNullIsAbsent pins the spec §5.1 sentence "An explicit JSON
// null is read as the argument being absent" for every typed argument
// shape ask_new takes (string, enum, integer, object, array): a call with
// nulls stores what the same call without those keys stores.
func TestExplicitNullIsAbsent(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	withNulls := vtoNew(t, root, map[string]any{
		"title": "same", "body": nil, "urgency": nil, "filed_by": nil, "recipient": nil,
		"tracker_ref": nil, "verifier": nil, "links": nil, "blocks": nil,
	})
	omitted := vtoNew(t, root, map[string]any{"title": "same"})

	// Everything but identity and time must match.
	withNulls.ID, omitted.ID = "", ""
	withNulls.CreatedAt = omitted.CreatedAt
	a, _ := json.Marshal(withNulls)
	b, _ := json.Marshal(omitted)
	if string(a) != string(b) {
		t.Fatalf("null arguments and omitted arguments stored different items:\n null:    %s\n omitted: %s", a, b)
	}
	if omitted.Urgency != core.UrgencyNormal || omitted.Verifier != nil {
		t.Fatalf("omitted-argument item is not the documented default: %s", b)
	}
}

// TestExplicitNullForRequiredArgumentIsMissing: absent means absent for a
// required argument too — null does not satisfy `required`, it is refused
// with code 2 like an omitted one, and nothing is written.
func TestExplicitNullForRequiredArgumentIsMissing(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	seed := mcpupdNew(t, root, "seed", "body", 1)
	before := mcpdriftSnapshot(t, root)

	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"ask_new", map[string]any{"title": nil}, "title"},
		{"ask_show", map[string]any{"id": nil}, "id"},
		{"ask_resolve", map[string]any{"id": nil, "note": "n"}, "id"},
		{"ask_update", map[string]any{"id": nil, "title": "x"}, "id"},
		{"ask_new", map[string]any{"title": "t", "verifier": map[string]any{"command": nil}}, "verifier.command"},
		{"ask_new", map[string]any{"title": "t", "links": []any{map[string]any{"label": "l", "url": nil}}}, "url"},
	}
	for _, tc := range cases {
		tr := mcpdriftCall(t, root, tc.tool, tc.args)
		if !tr.IsError {
			t.Fatalf("%s %v: accepted a null required argument: %.200s", tc.tool, tc.args, tr.Content[0].Text)
		}
		env := mcpconstraintErr(t, tr)
		if env.Code != 2 || !strings.Contains(env.Message, tc.want) || !strings.Contains(env.Message, "required") {
			t.Fatalf("%s %v: got code %d %q, want code 2 naming %q as required", tc.tool, tc.args, env.Code, env.Message, tc.want)
		}
	}
	mcpdriftSameStore(t, root, before)

	// And null for an optional argument of a mutating tool leaves the
	// field alone, exactly as omitting it does: body:null is not "clear
	// the body" (that is the explicit empty string, §5.4.1).
	tr := mcpdriftCall(t, root, "ask_update", map[string]any{"id": seed.ID, "title": "renamed", "body": nil, "body_append": nil})
	if tr.IsError {
		t.Fatalf("ask_update with null optionals refused: %.200s", tr.Content[0].Text)
	}
	got := mcpupdShow(t, root, seed.ID, 3)
	if got.Title != "renamed" || got.Body != "body" {
		t.Fatalf("after update with body:null: title=%q body=%q, want renamed/body", got.Title, got.Body)
	}
}
