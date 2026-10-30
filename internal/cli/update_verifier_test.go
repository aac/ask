package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// Tests for `ask update --verifier` / `--verifier-clear` (act-89671a): a
// stale or wrong verifier is fixed in place rather than by hand-editing
// the item JSON or resolve-and-refiling.

// seedWithVerifier stores one open item carrying a shell verifier.
func seedWithVerifier(t *testing.T, id, cmd string, timeout int) string {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	it := core.NewItem(id, "t", core.UrgencyNormal, core.StatusOpen, time.Now().UTC())
	it.Body = "body"
	it.Verifier = &core.Verifier{Type: core.VerifierShell, Command: cmd, TimeoutSeconds: timeout}
	seedStore(t, dir, it)
	return dir
}

// TestUpdateVerifierReplacesCommand is the accept criterion: the new
// command lands and shows up in `ask show --json`; the timeout the filer
// chose is kept, and the item stays open.
func TestUpdateVerifierReplacesCommand(t *testing.T) {
	dir := seedWithVerifier(t, "ask-a111", "op item get 'wrong item'", 120)

	if code := Run([]string{"update", "ask-a111", "--verifier", "true"}); code != 0 {
		t.Fatalf("update exit %d, want 0", code)
	}
	got := loadItem(t, dir, "ask-a111")
	if got.Verifier == nil || got.Verifier.Command != "true" {
		t.Fatalf("verifier %+v, want command \"true\"", got.Verifier)
	}
	if got.Verifier.TimeoutSeconds != 120 || got.Verifier.Type != core.VerifierShell {
		t.Fatalf("verifier lost its type/timeout: %+v", got.Verifier)
	}
	if got.Status != core.StatusOpen || got.Body != "body" {
		t.Fatalf("update touched other fields: status %s body %q", got.Status, got.Body)
	}

	out := captureStdout(t, func() {
		if code := Run([]string{"show", "ask-a111", "--json"}); code != 0 {
			t.Fatalf("show exit %d", code)
		}
	})
	if !strings.Contains(out, `"command": "true"`) {
		t.Fatalf("show --json does not carry the new verifier: %s", out)
	}
}

// An item filed without a verifier gains one with the default timeout,
// exactly as `ask new --verifier` would have given it.
func TestUpdateVerifierAddsWhenAbsent(t *testing.T) {
	dir := seedOne(t, "ask-a222", "t", "body")
	if code := Run([]string{"update", "ask-a222", "--verifier", "test -f /tmp/x"}); code != 0 {
		t.Fatalf("update exit %d, want 0", code)
	}
	got := loadItem(t, dir, "ask-a222")
	want := core.Verifier{Type: core.VerifierShell, Command: "test -f /tmp/x", TimeoutSeconds: core.DefaultVerifierTimeoutSeconds}
	if got.Verifier == nil || *got.Verifier != want {
		t.Fatalf("verifier %+v, want %+v", got.Verifier, want)
	}
}

func TestUpdateVerifierClear(t *testing.T) {
	dir := seedWithVerifier(t, "ask-a333", "sw_vers | grep -q 26.0.1", 60)
	out := captureStdout(t, func() {
		if code := Run([]string{"update", "ask-a333", "--verifier-clear", "--json"}); code != 0 {
			t.Fatalf("update exit %d, want 0", code)
		}
	})
	if !strings.Contains(out, `"verifier": null`) {
		t.Fatalf("json output should show verifier null: %s", out)
	}
	if got := loadItem(t, dir, "ask-a333"); got.Verifier != nil {
		t.Fatalf("verifier still present: %+v", got.Verifier)
	}

	// Clearing again is the idempotent no-op (exit 6).
	var code int
	captureStderr(t, func() {
		captureStdout(t, func() { code = Run([]string{"update", "ask-a333", "--verifier-clear"}) })
	})
	if code != 6 {
		t.Fatalf("second clear exit %d, want 6", code)
	}
}

// Setting the command the verifier already has writes nothing (exit 6).
func TestUpdateVerifierNoOp(t *testing.T) {
	seedWithVerifier(t, "ask-a444", "true", 60)
	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() { code = Run([]string{"update", "ask-a444", "--verifier", "true"}) })
	})
	if code != 6 || !strings.Contains(stderr, "already reads as requested") {
		t.Fatalf("exit %d stderr %q, want 6 + no-op warning", code, stderr)
	}
}

func TestUpdateVerifierValidation(t *testing.T) {
	dir := seedWithVerifier(t, "ask-a555", "keep me", 60)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"both", []string{"update", "ask-a555", "--verifier", "true", "--verifier-clear"}, "mutually exclusive"},
		{"empty", []string{"update", "ask-a555", "--verifier", ""}, "--verifier-clear"},
		{"whitespace", []string{"update", "ask-a555", "--verifier", "   "}, "must not be empty"},
		{"clear=false alone", []string{"update", "ask-a555", "--verifier-clear=false"}, "nothing to update"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			stderr := captureStderr(t, func() { code = Run(tc.args) })
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d stderr %q, want 2 + %q", code, stderr, tc.want)
			}
		})
	}
	if got := loadItem(t, dir, "ask-a555"); got.Verifier == nil || got.Verifier.Command != "keep me" {
		t.Fatalf("a refused update changed the verifier: %+v", got.Verifier)
	}
}
