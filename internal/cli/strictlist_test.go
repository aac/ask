package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// strictlistIDs decodes `ask list --json` stdout and returns the ids in it,
// failing the test if stdout is not a JSON array of items.
func strictlistIDs(t *testing.T, args []string, out string) []string {
	t.Helper()
	var items []core.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("%v: stdout is not a JSON array: %v\n%s", args, err, out)
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

// TestStrictlistAcceptance is act-9b2e23's acceptance (spec §7.2): one
// readable item and one schema_version "2" item. Without --strict the list
// is partial and the exit is 0, as before. With --strict stdout is
// byte-identical and the exit is 5 — the signal a --json consumer can
// observe without reading stderr.
func TestStrictlistAcceptance(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
	want := listskipV2(t, dir, "ask-bbbb")

	for _, base := range [][]string{{"list", "--json"}, {"list"}} {
		code, out, errOut := schemaverRun(t, base...)
		if code != 0 {
			t.Fatalf("%v: exit %d, want 0 (stderr %q)", base, code, errOut)
		}
		strict := append(append([]string{}, base...), "--strict")
		sCode, sOut, sErr := schemaverRun(t, strict...)
		if sCode != 5 {
			t.Fatalf("%v: exit %d, want 5 (stderr %q)", strict, sCode, sErr)
		}
		if sOut != out {
			t.Fatalf("%v: stdout must equal the non-strict stdout\n--- without\n%s\n--- with\n%s", strict, out, sOut)
		}
		if !strings.Contains(sOut, "ask-aaaa") || strings.Contains(sOut, "ask-bbbb") {
			t.Fatalf("%v: stdout must hold the readable item only, got %q", strict, sOut)
		}
		if !strings.HasPrefix(sErr, errOut) || !strings.Contains(errOut, "ask list: skipped ask-bbbb: ") {
			t.Fatalf("%v: stderr must start with the same per-id warning %q, got %q", strict, errOut, sErr)
		}
		if !strings.Contains(sErr, "ask list: --strict: 1 unreadable item file(s) skipped; the list is incomplete\n") {
			t.Fatalf("%v: stderr must say why the exit is 5, got %q", strict, sErr)
		}
	}

	_, out, _ := schemaverRun(t, "list", "--json", "--strict")
	if ids := strictlistIDs(t, []string{"list", "--json", "--strict"}, out); len(ids) != 1 || ids[0] != "ask-aaaa" {
		t.Fatalf("--json --strict: want [ask-aaaa], got %v", ids)
	}
	listskipSameBytes(t, dir, "ask-bbbb", want)
}

// TestStrictlistEmptyMeansNone is the case the flag exists for: a filter
// that matches no readable item prints [] either way, and only --strict
// tells a consumer that [] is not the whole answer.
func TestStrictlistEmptyMeansNone(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa", "ask-bbbb")
	listskipV2(t, dir, "ask-bbbb")

	args := []string{"list", "--status", "open", "--urgency", "blocker", "--json"}
	code, out, _ := schemaverRun(t, args...)
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("%v: want exit 0 and [], got exit %d stdout %q", args, code, out)
	}
	args = append(args, "--strict")
	code, out, _ = schemaverRun(t, args...)
	if code != 5 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("%v: want exit 5 and [], got exit %d stdout %q", args, code, out)
	}
}

// TestStrictlistNothingSkippedChangesNothing: on a store with no unreadable
// file --strict is invisible — same exit, stdout and stderr — including
// the empty store and a filter that matches nothing.
func TestStrictlistNothingSkippedChangesNothing(t *testing.T) {
	for name, ids := range map[string][]string{"two items": {"ask-aaaa", "ask-bbbb"}, "empty store": nil} {
		t.Run(name, func(t *testing.T) {
			listskipSeed(t, ids...)
			for _, base := range [][]string{{"list"}, {"list", "--json"}, {"list", "--json", "--status", "closed"}} {
				code, out, errOut := schemaverRun(t, base...)
				strict := append(append([]string{}, base...), "--strict")
				sCode, sOut, sErr := schemaverRun(t, strict...)
				if code != 0 || sCode != 0 || sOut != out || sErr != errOut || sErr != "" {
					t.Fatalf("%v: want exit 0 and output identical to %v; got exit %d/%d stdout %q/%q stderr %q/%q",
						strict, base, code, sCode, out, sOut, errOut, sErr)
				}
			}
		})
	}
}

// TestStrictlistAllUnreadableUnchanged: the no-readable-item case was
// already exit 5 with empty stdout; --strict adds nothing to it.
func TestStrictlistAllUnreadableUnchanged(t *testing.T) {
	dir := listskipSeed(t, "ask-aaaa")
	listskipV2(t, dir, "ask-aaaa")
	code, out, errOut := schemaverRun(t, "list", "--json")
	sCode, sOut, sErr := schemaverRun(t, "list", "--json", "--strict")
	if code != 5 || sCode != 5 || out != "" || sOut != "" || sErr != errOut {
		t.Fatalf("want identical exit-5 runs; got exit %d/%d stdout %q/%q stderr %q/%q", code, sCode, out, sOut, errOut, sErr)
	}
}

// TestStrictlistHelpNamesTheFlag: `ask list --help` documents --strict.
func TestStrictlistHelpNamesTheFlag(t *testing.T) {
	code, out, _ := schemaverRun(t, "list", "--help")
	if code != 0 || !strings.Contains(out, "-strict") || !strings.Contains(out, "exit 5") {
		t.Fatalf("list --help: want exit 0 and a -strict entry naming exit 5, got exit %d\n%s", code, out)
	}
}
