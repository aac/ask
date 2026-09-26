package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Friday 2026-09-25, 21:58 in the filer's zone.
var lintNow = time.Date(2026, 9, 25, 21, 58, 0, 0, time.FixedZone("MDT", -6*3600))

func TestRelativeDateWarnings(t *testing.T) {
	cases := []struct {
		name string
		text string
		n    int      // expected number of warnings
		want []string // substrings the warnings must contain
	}{
		{"tomorrow resolves to filing date +1", "Tomorrow: run the v0 dogfood", 1, []string{`"Tomorrow"`, "Tomorrow = 2026-09-26"}},
		{"today", "needs doing today", 1, []string{"today = 2026-09-25"}},
		{"tonight", "deploy tonight", 1, []string{"tonight = 2026-09-25"}},
		{"yesterday", "broke yesterday", 1, []string{"yesterday = 2026-09-24"}},
		{"last night", "failed last night", 1, []string{"last night = 2026-09-24"}},
		{"this week is the filing week's Monday", "land it this week", 1, []string{"this week = the week of 2026-09-21"}},
		{"next week", "review next week", 1, []string{"next week = the week of 2026-09-28"}},
		{"in N days", "expires in 3 days", 1, []string{"in 3 days = 2026-09-28"}},
		{"N days ago", "filed 10 days ago", 1, []string{"10 days ago = 2026-09-15"}},
		{"bare weekday", "call the bank Monday", 1, []string{`"Monday"`, "Monday 2026-09-28"}},
		{"weekday case-insensitive", "by friday", 1, []string{"Friday 2026-10-02"}},
		{"two phrases, deduped", "today or tomorrow, not TODAY", 2, []string{"today = ", "tomorrow = "}},
		{"title and body both scanned", "Renew cert\nbefore next week", 1, []string{"next week"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := relativeDateWarnings(c.text, lintNow)
			if len(got) != c.n {
				t.Fatalf("got %d warnings %q, want %d", len(got), got, c.n)
			}
			joined := strings.Join(got, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("warnings %q missing %q", got, w)
				}
			}
		})
	}
}

// Negative cases: absolute dates, anchored weekdays, recurring days, and words
// that merely contain a relative word must not warn.
func TestRelativeDateWarningsNegative(t *testing.T) {
	for _, text := range []string{
		"Run the v0 dogfood on 2026-09-26",
		"Call the bank Monday 2026-09-28",
		"Call the bank Mon, Sep 28 — Monday Sep 28",
		"Wednesday, September 30 works",
		"Standup every Monday",
		"Backups run on Sundays",
		"todays_report.csv and yesterdays.log", // no word boundary match
		"Set up Gmail OAuth",
		"",
	} {
		if got := relativeDateWarnings(text, lintNow); len(got) != 0 {
			t.Errorf("%q: unexpected warnings %q", text, got)
		}
	}
}

// End to end: ask new still files the item (exit 0, id on stdout) and the
// warning goes to stderr only.
func TestNewWarnsButFiles(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)
	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run([]string{"new", "Tomorrow: run the dogfood"})
		})
	})
	if code != 0 {
		t.Fatalf("new exit %d, want 0 (warn, not refuse)", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "ask-") || strings.Contains(stdout, "warning") {
		t.Errorf("stdout should be the id alone, got %q", stdout)
	}
	if !strings.Contains(stderr, `ask new: warning: "Tomorrow" is relative`) {
		t.Errorf("stderr missing warning, got %q", stderr)
	}
}
