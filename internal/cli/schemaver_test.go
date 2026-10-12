package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// schemaverSeed builds a store holding one ordinary v1 item (ask-aaaa) and
// one item (ask-bbbb) that a newer ask is imagined to have written: the
// file ask itself wrote, hand-edited to schema_version "2" plus a key v1
// does not know. Returns the store dir (already chdir'd into) and the
// v2 file's path and exact bytes.
func schemaverSeed(t *testing.T) (dir, path string, want []byte) {
	t.Helper()
	dir = t.TempDir()
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	seedStore(t, dir,
		core.NewItem("ask-aaaa", "ordinary", core.UrgencyNormal, core.StatusOpen, base),
		core.NewItem("ask-bbbb", "strip probe", core.UrgencyNormal, core.StatusOpen, base),
	)
	path = filepath.Join(dir, ".ask", "items", "ask-bbbb.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const from = `"schema_version": "1",`
	if !bytes.Contains(b, []byte(from)) {
		t.Fatalf("fixture premise broken: no %s in\n%s", from, b)
	}
	want = bytes.Replace(b, []byte(from),
		[]byte(`"schema_version": "2",`+"\n"+`  "subscriptions": ["human:someone"],`), 1)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	inDir(t, dir)
	return dir, path, want
}

// schemaverRun runs one ask invocation and returns its exit code, stdout
// and stderr.
func schemaverRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { code = Run(args) })
	})
	return code, stdout, stderr
}

// TestSchemaverShowExits5 is the read half of act-fc0ec7's acceptance:
// `ask show` on a schema_version "2" item exits 5, names schema_version on
// stderr, and prints nothing on stdout (spec §1.1.1, §1.10).
func TestSchemaverShowExits5(t *testing.T) {
	_, _, _ = schemaverSeed(t)
	for _, args := range [][]string{{"show", "ask-bbbb"}, {"show", "--json", "ask-bbbb"}} {
		code, out, errOut := schemaverRun(t, args...)
		if code != 5 {
			t.Fatalf("%v: exit %d, want 5 (stderr %q)", args, code, errOut)
		}
		if !strings.Contains(errOut, "schema_version") || !strings.Contains(errOut, "ask-bbbb") {
			t.Fatalf("%v: stderr must name schema_version and the id, got %q", args, errOut)
		}
		if out != "" {
			t.Fatalf("%v: stdout must be empty on error, got %q", args, out)
		}
	}
	// The ordinary item next to it is unaffected.
	if code, _, errOut := schemaverRun(t, "show", "ask-aaaa"); code != 0 {
		t.Fatalf("show of the v1 sibling: exit %d (stderr %q)", code, errOut)
	}
}

// TestSchemaverWriteVerbsLeaveFileByteIdentical is the write half: every
// verb that would rewrite the item exits 5 naming schema_version and the
// file — including the key v1 would have dropped — is byte-identical.
func TestSchemaverWriteVerbsLeaveFileByteIdentical(t *testing.T) {
	_, path, want := schemaverSeed(t)
	for _, args := range [][]string{
		{"resolve", "ask-bbbb"},
		{"resolve", "ask-bbbb", "--note", "done"},
		{"close", "ask-bbbb"},
		{"reopen", "ask-bbbb"},
		{"update", "ask-bbbb", "--body", "rewritten"},
		{"update", "ask-bbbb", "--body-append", "more"},
	} {
		code, out, errOut := schemaverRun(t, args...)
		if code != 5 {
			t.Fatalf("%v: exit %d, want 5 (stdout %q stderr %q)", args, code, out, errOut)
		}
		if !strings.Contains(errOut, "schema_version") {
			t.Fatalf("%v: stderr must name schema_version, got %q", args, errOut)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%v rewrote the item:\n--- want\n%s\n--- got\n%s", args, want, got)
		}
	}
}

// TestSchemaverListDoesNotPresentItAsNormal pins what `ask list` does over
// a store holding one such item next to an ordinary one: it never prints
// the item as an ordinary v1 row, and says so on stderr naming the id and
// schema_version. Since act-d1ad86 that is a skip-and-warn (spec §7.2) —
// the ordinary sibling is still listed and the exit is 0 — where it used
// to abort the whole listing with exit 5. The file is left byte-identical.
func TestSchemaverListDoesNotPresentItAsNormal(t *testing.T) {
	_, path, want := schemaverSeed(t)
	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "--all"}} {
		code, out, errOut := schemaverRun(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, want 0 (stderr %q)", args, code, errOut)
		}
		if !strings.Contains(errOut, "schema_version") || !strings.Contains(errOut, "ask-bbbb") {
			t.Fatalf("%v: stderr must name the id and schema_version, got %q", args, errOut)
		}
		if strings.Contains(out, "ask-bbbb") {
			t.Fatalf("%v: listed the unsupported item as a normal row: %q", args, out)
		}
		if !strings.Contains(out, "ask-aaaa") {
			t.Fatalf("%v: the v1 sibling must still be listed, got %q", args, out)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%v rewrote the item:\n%s", args, got)
		}
	}
}
