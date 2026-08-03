package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// chdir points the process at dir for the duration of the test. The CLI
// verbs resolve the store from the working directory, so every end-to-end
// exit-code assertion needs this.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// treeOf lists every path under root, relative and sorted.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(out)
	return out
}

// TestReadCommandsDoNotCreateStore is act-55ae5b acceptance criterion 1 at
// the CLI boundary: a read in a directory with no store leaves the
// filesystem unchanged — no .ask/ — while still exiting 5 with a clear
// message. Every read verb is covered, since a husk from any one of them
// is as permanent as a husk from `ask list`.
func TestReadCommandsDoNotCreateStore(t *testing.T) {
	for _, args := range [][]string{
		{"list"},
		{"list", "--status", "open"},
		{"show", "ask-1234"},
		{"resolve", "ask-1234"},
		{"reopen", "ask-1234"},
		{"close", "ask-1234"},
		{"new", "a title"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			before := treeOf(t, dir)

			var code int
			stderr := captureStderr(t, func() { code = Run(args) })

			if code != 5 {
				t.Fatalf("%v: exit %d, want 5", args, code)
			}
			if !strings.Contains(stderr, "ask not initialized") {
				t.Fatalf("%v: stderr %q, want the not-initialized message", args, stderr)
			}
			if after := treeOf(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatalf("%v mutated the filesystem: before=%v after=%v", args, before, after)
			}
			if _, err := os.Stat(filepath.Join(dir, ".ask")); !os.IsNotExist(err) {
				t.Fatalf("%v created .ask/ (stat err: %v)", args, err)
			}
		})
	}
}

// TestStrandedStoreExitCode is act-55ae5b acceptance criterion 2: a store
// with item files but no config.json is distinguishable from an
// empty/absent one through the CLI's exit code alone — 7 vs 5 — so a
// consumer never has to count .ask/items/ itself to tell a full inbox
// from no inbox.
func TestStrandedStoreExitCode(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedStore(t, dir,
		core.NewItem("ask-aaaa", "still needs a human", core.UrgencyBlocker, core.StatusOpen, time.Now().UTC()),
	)
	if err := os.Remove(filepath.Join(dir, ".ask", "config.json")); err != nil {
		t.Fatalf("remove config: %v", err)
	}

	var code int
	stderr := captureStderr(t, func() { code = Run([]string{"list"}) })
	if code != 7 {
		t.Fatalf("stranded store: exit %d, want 7", code)
	}
	if !strings.Contains(stderr, "stranded") || !strings.Contains(stderr, "1 item file") {
		t.Fatalf("stderr should name the condition and the count, got %q", stderr)
	}

	// The empty case, in the same shape, must still be 5 — that contrast is
	// the whole signal.
	empty := t.TempDir()
	chdir(t, empty)
	if code := Run([]string{"list"}); code != 5 {
		t.Fatalf("empty dir: exit %d, want 5", code)
	}
}

// TestInitRecoversStrandedStore: exit 7 tells a consumer what to do, and
// `ask init` is that repair — it adopts the orphaned items rather than
// starting a fresh store beside them.
func TestInitRecoversStrandedStore(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedStore(t, dir,
		core.NewItem("ask-bbbb", "stranded", core.UrgencyNormal, core.StatusOpen, time.Now().UTC()),
	)
	if err := os.Remove(filepath.Join(dir, ".ask", "config.json")); err != nil {
		t.Fatalf("remove config: %v", err)
	}

	if code := Run([]string{"init"}); code != 0 {
		t.Fatalf("init on stranded store: exit %d, want 0", code)
	}
	out := captureStdout(t, func() {
		if code := Run([]string{"list"}); code != 0 {
			t.Fatalf("list after repair: exit %d, want 0", code)
		}
	})
	if !strings.Contains(out, "ask-bbbb") {
		t.Fatalf("repaired store should list the preserved item, got %q", out)
	}
}
