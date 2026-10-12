package cli

import (
	"strings"
	"testing"
)

// act-23daf6: `ask list`'s skip warning for a stray file (one whose name
// is not a valid id) names the file. The line keeps its shape,
// `ask list: skipped <id>: <reason>`; only the reason differs, and only
// for strays.

// TestListfixStrayWarningNamesTheFile is the ticket's acceptance: a store
// with a real item and a hand-placed ASK-AAAB.json.
func TestListfixStrayWarningNamesTheFile(t *testing.T) {
	root := t.TempDir()
	strayStore(t, root, "01HXMAIN0000000000000000A")
	inDir(t, root)

	code, stdout, stderr := idguardRun(t, "list")
	if code != 0 || !strings.Contains(stdout, "ask-aaaa") {
		t.Fatalf("list: exit %d, stdout %q (stderr %q)", code, stdout, stderr)
	}
	const want = "ask list: skipped ASK-AAAB: invalid ask id: stray file in .ask/items/ not named for an id: ASK-AAAB.json\n"
	if stderr != want {
		t.Fatalf("stray warning:\n got %q\nwant %q", stderr, want)
	}
}

// TestListfixValidlyNamedSkipUnchanged: a validly-named but unreadable
// item (schema_version "2") keeps the warning it always had.
func TestListfixValidlyNamedSkipUnchanged(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
	listskipV2(t, dir, "ask-bbbb")
	inDir(t, dir)

	code, _, stderr := idguardRun(t, "list")
	const want = "ask list: skipped ask-bbbb: item ask-bbbb: unsupported schema_version \"2\": this ask reads and writes only schema_version \"1\" (the item was probably written by a newer ask; upgrade ask, do not edit the file)\n"
	if code != 0 || stderr != want {
		t.Fatalf("exit %d, warning:\n got %q\nwant %q", code, stderr, want)
	}
}
