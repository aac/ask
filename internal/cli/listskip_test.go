package cli

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

// listskipV2 turns the item file ask itself wrote for id into one a newer
// ask is imagined to have written (schema_version "2"); listskipCorrupt
// truncates it to something that is not JSON. Both return the bytes now on
// disk so a test can assert nothing rewrote them.
func listskipV2(t *testing.T, dir, id string) []byte {
	t.Helper()
	p := filepath.Join(dir, ".ask", "items", id+".json")
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

func listskipCorrupt(t *testing.T, dir, id string) []byte {
	t.Helper()
	b := []byte(`{"id": "` + id)
	if err := os.WriteFile(filepath.Join(dir, ".ask", "items", id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// listskipSeed builds a store of open normal items with the given ids and
// chdirs into it.
func listskipSeed(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	items := make([]*core.Item, len(ids))
	for i, id := range ids {
		items[i] = core.NewItem(id, "title of "+id, core.UrgencyNormal, core.StatusOpen, base)
	}
	seedStore(t, dir, items...)
	inDir(t, dir)
	return dir
}

func listskipSameBytes(t *testing.T, dir, id string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, ".ask", "items", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s was rewritten:\n--- want\n%s\n--- got\n%s", id, want, got)
	}
}

// TestListskipAcceptance is act-d1ad86's CLI acceptance: two items, one
// hand-set to schema_version 2. `ask list` prints the readable item, warns
// on stderr naming the skipped id, exits 0.
func TestListskipAcceptance(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
	want := listskipV2(t, dir, "ask-bbbb")

	code, out, errOut := schemaverRun(t, "list")
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", code, errOut)
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); len(got) != 1 || !strings.HasPrefix(got[0], "ask-aaaa\t") {
		t.Fatalf("stdout: want exactly the ask-aaaa row, got %q", out)
	}
	wantWarn := "ask list: skipped ask-bbbb: "
	if !strings.HasPrefix(errOut, wantWarn) || !strings.Contains(errOut, "schema_version") || strings.Count(errOut, "\n") != 1 {
		t.Fatalf("stderr: want one line starting %q naming schema_version, got %q", wantWarn, errOut)
	}
	listskipSameBytes(t, dir, "ask-bbbb", want)
}

// TestListskipMixedCorruptAndV2 covers both kinds of unreadable file at
// once, in plain and --json form. Warnings are one stderr line per skipped
// id, in id order; stdout is only the list — under --json a valid JSON
// array holding just the readable item.
func TestListskipMixedCorruptAndV2(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb", "ask-cccc")
	wantV2 := listskipV2(t, dir, "ask-cccc")
	wantCorrupt := listskipCorrupt(t, dir, "ask-bbbb")

	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "--all"}, {"list", "--status", "open"}} {
		code, out, errOut := schemaverRun(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, want 0 (stderr %q)", args, code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 2 ||
			!strings.HasPrefix(lines[0], "ask list: skipped ask-bbbb: ") || !strings.Contains(lines[0], "corrupt") ||
			!strings.HasPrefix(lines[1], "ask list: skipped ask-cccc: ") || !strings.Contains(lines[1], "schema_version") {
			t.Fatalf("%v: stderr: want one warning per skipped id in id order, got %q", args, errOut)
		}
		if strings.Contains(out, "ask-bbbb") || strings.Contains(out, "ask-cccc") || strings.Contains(out, "skipped") {
			t.Fatalf("%v: stdout must hold only the readable item, got %q", args, out)
		}
		if !strings.Contains(out, "ask-aaaa") {
			t.Fatalf("%v: readable item missing from stdout: %q", args, out)
		}
		if args[len(args)-1] == "--json" {
			var items []core.Item
			if err := json.Unmarshal([]byte(out), &items); err != nil {
				t.Fatalf("--json stdout is not a JSON array: %v\n%s", err, out)
			}
			if len(items) != 1 || items[0].ID != "ask-aaaa" {
				t.Fatalf("--json: want [ask-aaaa], got %+v", items)
			}
		}
	}
	listskipSameBytes(t, dir, "ask-bbbb", wantCorrupt)
	listskipSameBytes(t, dir, "ask-cccc", wantV2)
}

// TestListskipAllUnreadable: with every item file unreadable `ask list`
// exits 5, still warns per id, and prints nothing on stdout — in
// particular not an empty JSON array, which would read as an empty inbox.
func TestListskipAllUnreadable(t *testing.T) {
	for name, breakIt := range map[string]func(t *testing.T, dir string){
		"all schema_version 2": func(t *testing.T, dir string) {
			listskipV2(t, dir, "ask-aaaa")
			listskipV2(t, dir, "ask-bbbb")
		},
		"all corrupt": func(t *testing.T, dir string) {
			listskipCorrupt(t, dir, "ask-aaaa")
			listskipCorrupt(t, dir, "ask-bbbb")
		},
		"one of each": func(t *testing.T, dir string) {
			listskipCorrupt(t, dir, "ask-aaaa")
			listskipV2(t, dir, "ask-bbbb")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
			breakIt(t, dir)
			for _, args := range [][]string{{"list"}, {"list", "--json"}} {
				code, out, errOut := schemaverRun(t, args...)
				if code != 5 {
					t.Fatalf("%v: exit %d, want 5 (stderr %q)", args, code, errOut)
				}
				if out != "" {
					t.Fatalf("%v: stdout must be empty, got %q", args, out)
				}
				for _, want := range []string{
					"ask list: skipped ask-aaaa: ",
					"ask list: skipped ask-bbbb: ",
					"ask list: no readable items (2 unreadable)",
				} {
					if !strings.Contains(errOut, want) {
						t.Fatalf("%v: stderr missing %q, got %q", args, want, errOut)
					}
				}
			}
		})
	}
}

// TestListskipEmptyStoreStillExits0: an initialized store with no item
// files is an empty inbox, not an unreadable one — exit 0, no warning,
// `[]` under --json.
func TestListskipEmptyStoreStillExits0(t *testing.T) {
	listskipSeed(t)
	code, out, errOut := schemaverRun(t, "list")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("list: want exit 0 and no output, got exit %d stdout %q stderr %q", code, out, errOut)
	}
	code, out, errOut = schemaverRun(t, "list", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" || errOut != "" {
		t.Fatalf("list --json: want exit 0 and [], got exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// TestListskipFilterExcludesOnlyReadable pins the detail §7.2 leaves open:
// readability is judged before the filters run. One readable item that the
// filter excludes plus one unreadable file is exit 0 with an empty list
// and the warning — not exit 5.
func TestListskipFilterExcludesOnlyReadable(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
	listskipV2(t, dir, "ask-bbbb")

	code, out, errOut := schemaverRun(t, "list", "--json", "--status", "closed")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("want exit 0 and [], got exit %d stdout %q stderr %q", code, out, errOut)
	}
	if !strings.Contains(errOut, "ask list: skipped ask-bbbb: ") {
		t.Fatalf("the skip must still be warned about, got %q", errOut)
	}
}

// TestListskipUnrelatedIDsStayReachable: one bad file must not make any
// other id unresolvable. Every verb that addresses the good item by id or
// by prefix still works, and the bad item still fails with exit 5 and is
// left byte-identical.
func TestListskipUnrelatedIDsStayReachable(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb", "ask-cccc")
	wantV2 := listskipV2(t, dir, "ask-bbbb")
	wantCorrupt := listskipCorrupt(t, dir, "ask-cccc")

	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"show", "ask-aaaa"}, 0},
		{[]string{"show", "aaa"}, 0}, // prefix resolution scans every id
		{[]string{"update", "ask-aaaa", "--body", "edited"}, 0},
		{[]string{"resolve", "ask-aaaa"}, 0},
		{[]string{"reopen", "ask-aaaa"}, 0},
		{[]string{"close", "ask-aaaa"}, 0},
		{[]string{"new", "filed next to bad files"}, 0},
		{[]string{"show", "ask-bbbb"}, 5},
		{[]string{"show", "ask-cccc"}, 5},
		{[]string{"resolve", "ask-cccc"}, 5},
		{[]string{"update", "ask-cccc", "--body", "x"}, 5},
	} {
		code, out, errOut := schemaverRun(t, c.args...)
		if code != c.want {
			t.Fatalf("%v: exit %d, want %d (stdout %q stderr %q)", c.args, code, c.want, out, errOut)
		}
	}
	listskipSameBytes(t, dir, "ask-bbbb", wantV2)
	listskipSameBytes(t, dir, "ask-cccc", wantCorrupt)
}
