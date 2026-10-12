package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// act-5dd2e1: the 1024 cap on `--recipient` and on each `--blocks` ref
// counts characters (Unicode code points), as the title cap does and as
// the ask_new schema's maxLength reads, not bytes.

// TestNewRefCapsCountCharacters: 1024 non-ASCII characters (2048 or 3072
// bytes) is accepted and stored whole; 1025 exits 2 and writes nothing.
func TestNewRefCapsCountCharacters(t *testing.T) {
	for _, flagName := range []string{"--recipient", "--blocks"} {
		for _, ch := range []string{"é", "日"} {
			dir := lenlimStore(t)
			var code int
			stderr := captureStderr(t, func() {
				code = Run([]string{"new", "t", flagName, strings.Repeat(ch, 1025)})
			})
			if code != 2 {
				t.Fatalf("%s %q x1025: exit %d, want 2", flagName, ch, code)
			}
			if want := strings.TrimPrefix(flagName, "--") + ": ref exceeds 1024 characters"; !strings.Contains(stderr, want) {
				t.Fatalf("%s %q x1025: stderr %q, want it to contain %q", flagName, ch, stderr, want)
			}
			if n := len(lenlimItems(t, dir)); n != 0 {
				t.Fatalf("%s %q x1025: refused, yet %d item(s) written", flagName, ch, n)
			}

			if code := Run([]string{"new", "t", flagName, strings.Repeat(ch, 1024)}); code != 0 {
				t.Fatalf("%s %q x1024: exit %d, want 0", flagName, ch, code)
			}
			items := lenlimItems(t, dir)
			if len(items) != 1 {
				t.Fatalf("%s %q x1024: %d item(s), want 1", flagName, ch, len(items))
			}
			var stored string
			switch {
			case flagName == "--recipient" && items[0].Recipient != nil:
				stored = *items[0].Recipient
			case flagName == "--blocks" && len(items[0].Blocks) == 1:
				stored = items[0].Blocks[0]
			}
			if got := utf8.RuneCountInString(stored); got != 1024 {
				t.Fatalf("%s %q x1024: stored value is %d characters, want 1024", flagName, ch, got)
			}
		}
	}
}
