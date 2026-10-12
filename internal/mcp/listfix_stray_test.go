package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// act-23daf6, MCP half: for a stray file the prose part and the `_meta`
// reason name the file; the entry's `id` is still the file's name stem.
func TestListfixStraySkipNamesTheFile(t *testing.T) {
	root := t.TempDir()
	listskipSeed(t, root, "ask-aaaa")
	if err := os.WriteFile(filepath.Join(root, ".ask", "items", "ASK-AAAB.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	const reason = "invalid ask id: stray file in .ask/items/ not named for an id: ASK-AAAB.json"
	skipped := skipmetaSkipped(t, skipmetaResult(t, root, map[string]any{}))
	if len(skipped) != 1 || skipped[0].ID != "ASK-AAAB" || skipped[0].Reason != reason {
		t.Fatalf(`_meta["ask/skipped"]: want {ASK-AAAB, %q}, got %+v`, reason, skipped)
	}
	tr := listskipCall(t, root, map[string]any{})
	if ids := listskipItems(t, tr); len(ids) != 1 || ids[0] != "ask-aaaa" || len(tr.Content) != 2 {
		t.Fatalf("want the array then one prose line, got %+v", tr.Content)
	}
	if want := "ask list: skipped ASK-AAAB: " + reason; tr.Content[1].Text != want {
		t.Fatalf("prose line:\n got %q\nwant %q", tr.Content[1].Text, want)
	}
}
