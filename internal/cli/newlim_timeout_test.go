package cli

import (
	"strings"
	"testing"
)

// act-5387d0: `ask new --timeout` takes the spec §1.2 range, 1..3600,
// the one ask_new enforces. An omitted flag stores the default 60; a
// flag that was given is range-checked whatever its value, so an
// explicit 0 is refused rather than read as "default".

// newlimRunNew runs `ask new` with args in a fresh store and returns the
// exit code, stderr, and how many items the store holds afterwards.
func newlimRunNew(t *testing.T, args ...string) (code int, stderr string, items int) {
	t.Helper()
	dir := lenlimStore(t)
	stderr = captureStderr(t, func() {
		code = Run(append([]string{"new"}, args...))
	})
	return code, stderr, len(lenlimItems(t, dir))
}

// TestNewTimeoutOutOfRangeRefused: every explicit value outside 1..3600,
// 0 included, exits 2 naming the range and writes nothing.
func TestNewTimeoutOutOfRangeRefused(t *testing.T) {
	for _, v := range []string{"-5", "99999", "0", "3601", "-1"} {
		code, stderr, items := newlimRunNew(t, "t", "--verifier", "true", "--timeout", v)
		if code != 2 {
			t.Errorf("--timeout %s: exit %d, want 2", v, code)
		}
		if want := "ask new: --timeout must be between 1 and 3600 (got " + v + ")"; !strings.Contains(stderr, want) {
			t.Errorf("--timeout %s: stderr %q, want it to contain %q", v, stderr, want)
		}
		if items != 0 {
			t.Errorf("--timeout %s: refused, yet %d item(s) written", v, items)
		}
	}
	// The `--timeout=0` spelling is the same explicit 0.
	if code, _, items := newlimRunNew(t, "t", "--verifier", "true", "--timeout=0"); code != 2 || items != 0 {
		t.Errorf("--timeout=0: exit %d with %d item(s), want 2 and 0", code, items)
	}
}

// TestNewTimeoutInRangeAndOmitted: the two ends of the range are stored
// as given, and an omitted flag stores the default.
func TestNewTimeoutInRangeAndOmitted(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"t", "--verifier", "true", "--timeout", "1"}, 1},
		{[]string{"t", "--verifier", "true", "--timeout", "3600"}, 3600},
		{[]string{"t", "--verifier", "true"}, 60},
	} {
		dir := lenlimStore(t)
		if code := Run(append([]string{"new"}, tc.args...)); code != 0 {
			t.Fatalf("%v: exit %d, want 0", tc.args, code)
		}
		items := lenlimItems(t, dir)
		if len(items) != 1 || items[0].Verifier == nil {
			t.Fatalf("%v: want one item with a verifier, got %d item(s)", tc.args, len(items))
		}
		if got := items[0].Verifier.TimeoutSeconds; got != tc.want {
			t.Errorf("%v: stored timeout_seconds %d, want %d", tc.args, got, tc.want)
		}
	}
}
