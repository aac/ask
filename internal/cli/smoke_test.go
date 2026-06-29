package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSmokeHappyPath exercises the v1 CLI surface end-to-end via the public
// Run() dispatcher: init -> new (with verifier) -> list -> resolve -> close.
// It asserts each verb exits 0 and the seeded item makes it through every
// transition. Per Task 16 this is a functional-correctness smoke test, not a
// byte-for-byte format check — we assert behavior (exit code, item present
// by ID, on-disk file remains) rather than exact stdout shape so it stays
// stable as sibling tasks (e.g. act-7079) iterate on flag surface.
func TestSmokeHappyPath(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// init
	out := captureStdout(t, func() {
		if code := Run([]string{"init"}); code != 0 {
			t.Fatalf("init exit: %d", code)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, ".ask", "config.json")); err != nil {
		t.Fatalf("init did not create .ask/config.json: %v (stdout=%q)", err, out)
	}

	// new (with verifier so the verifier-attached branch is covered)
	captureStdout(t, func() {
		if code := Run([]string{"new", "Set up OAuth", "--urgency", "blocker", "--verifier", "true"}); code != 0 {
			t.Fatalf("new exit: %d", code)
		}
	})

	// One item on disk; capture its id from the items dir.
	files, err := os.ReadDir(filepath.Join(dir, ".ask", "items"))
	if err != nil {
		t.Fatalf("readdir items: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 item after new, got %d", len(files))
	}
	name := files[0].Name()
	if !strings.HasSuffix(name, ".json") {
		t.Fatalf("unexpected item file name %q", name)
	}
	id := strings.TrimSuffix(name, ".json")
	if !strings.HasPrefix(id, "ask-") {
		t.Fatalf("id %q does not have ask- prefix", id)
	}

	// list — must succeed and mention the new id.
	listOut := captureStdout(t, func() {
		if code := Run([]string{"list"}); code != 0 {
			t.Fatalf("list exit: %d", code)
		}
	})
	if !strings.Contains(listOut, id) {
		t.Fatalf("list output missing id %q:\n%s", id, listOut)
	}

	// resolve
	captureStdout(t, func() {
		if code := Run([]string{"resolve", id}); code != 0 {
			t.Fatalf("resolve exit: %d", code)
		}
	})

	// close
	captureStdout(t, func() {
		if code := Run([]string{"close", id}); code != 0 {
			t.Fatalf("close exit: %d", code)
		}
	})
}

// TestSmokeReopenPath exercises the open -> resolved -> open -> resolved ->
// closed cycle via Run(): init -> new -> resolve -> reopen -> resolve ->
// close. This complements TestSmokeHappyPath by walking the reopen edge of
// the state machine end-to-end through the public CLI surface.
func TestSmokeReopenPath(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	captureStdout(t, func() {
		if code := Run([]string{"init"}); code != 0 {
			t.Fatalf("init exit: %d", code)
		}
	})
	captureStdout(t, func() {
		if code := Run([]string{"new", "x"}); code != 0 {
			t.Fatalf("new exit: %d", code)
		}
	})

	files, err := os.ReadDir(filepath.Join(dir, ".ask", "items"))
	if err != nil {
		t.Fatalf("readdir items: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 item after new, got %d", len(files))
	}
	id := strings.TrimSuffix(files[0].Name(), ".json")

	captureStdout(t, func() {
		if code := Run([]string{"resolve", id}); code != 0 {
			t.Fatalf("first resolve exit: %d", code)
		}
	})
	captureStdout(t, func() {
		if code := Run([]string{"reopen", id, "--reason", "didn't work"}); code != 0 {
			t.Fatalf("reopen exit: %d", code)
		}
	})
	captureStdout(t, func() {
		if code := Run([]string{"resolve", id}); code != 0 {
			t.Fatalf("second resolve exit: %d", code)
		}
	})
	captureStdout(t, func() {
		if code := Run([]string{"close", id}); code != 0 {
			t.Fatalf("close exit: %d", code)
		}
	})
}
