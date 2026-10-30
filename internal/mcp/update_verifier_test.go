package mcp

import (
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// ask_update's verifier / verifier_clear (act-89671a): the MCP twin of
// `ask update --verifier` / `--verifier-clear`.

func TestAskUpdateVerifierSetAndClear(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "b", 1)

	// No verifier yet: one is added with the default timeout.
	if tr := mcpupdCall(t, root, 2, map[string]any{"id": it.ID, "verifier": "op item get x"}); tr.IsError {
		t.Fatalf("set verifier refused: %+v", tr.Content)
	}
	got := mcpupdShow(t, root, it.ID, 3)
	want := core.Verifier{Type: core.VerifierShell, Command: "op item get x", TimeoutSeconds: core.DefaultVerifierTimeoutSeconds}
	if got.Verifier == nil || *got.Verifier != want {
		t.Fatalf("verifier %+v, want %+v", got.Verifier, want)
	}

	// Replacing the command keeps the timeout.
	if tr := mcpupdCall(t, root, 4, map[string]any{"id": it.ID, "verifier": "true"}); tr.IsError {
		t.Fatalf("replace verifier refused: %+v", tr.Content)
	}
	got = mcpupdShow(t, root, it.ID, 5)
	if got.Verifier == nil || got.Verifier.Command != "true" || got.Verifier.TimeoutSeconds != core.DefaultVerifierTimeoutSeconds {
		t.Fatalf("verifier after replace %+v", got.Verifier)
	}
	if got.Status != core.StatusOpen || got.Body != "b" {
		t.Fatalf("update touched other fields: %s %q", got.Status, got.Body)
	}

	// Same command again: the two-part no-op envelope.
	if tr := mcpupdCall(t, root, 6, map[string]any{"id": it.ID, "verifier": "true"}); tr.IsError ||
		len(tr.Content) != 2 || !strings.Contains(tr.Content[1].Text, "already reads as requested") {
		t.Fatalf("no-op envelope: %+v", tr)
	}

	if tr := mcpupdCall(t, root, 7, map[string]any{"id": it.ID, "verifier_clear": true}); tr.IsError {
		t.Fatalf("clear refused: %+v", tr.Content)
	}
	if got = mcpupdShow(t, root, it.ID, 8); got.Verifier != nil {
		t.Fatalf("verifier not cleared: %+v", got.Verifier)
	}
}

func TestAskUpdateVerifierValidation(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "b", 1)
	if tr := mcpupdCall(t, root, 2, map[string]any{"id": it.ID, "verifier": "keep me"}); tr.IsError {
		t.Fatalf("seed verifier refused: %+v", tr.Content)
	}

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"both", map[string]any{"id": it.ID, "verifier": "true", "verifier_clear": true}, "mutually exclusive"},
		{"empty", map[string]any{"id": it.ID, "verifier": ""}, "verifier_clear"},
		{"whitespace", map[string]any{"id": it.ID, "verifier": "  "}, "must not be empty"},
		{"too long", map[string]any{"id": it.ID, "verifier": strings.Repeat("x", maxVerifierCommandLen+1)}, "exceeds"},
		{"clear false alone", map[string]any{"id": it.ID, "verifier_clear": false}, "nothing to update"},
		{"clear not bool", map[string]any{"id": it.ID, "verifier_clear": "yes"}, ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mcpupdWantErr(t, mcpupdCall(t, root, 10+i, tc.args), 2, tc.want)
		})
	}
	if got := mcpupdShow(t, root, it.ID, 99); got.Verifier == nil || got.Verifier.Command != "keep me" {
		t.Fatalf("a refused update changed the verifier: %+v", got.Verifier)
	}
}
