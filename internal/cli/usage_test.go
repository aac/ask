package cli

import (
	"strings"
	"testing"
)

// TestSubcommandHelpExitsZero asserts that `ask <cmd> --help` and the short
// form `ask <cmd> -h` exit 0 and print a usable usage block on stdout for
// every subcommand. Discovered via agent feedback (act-007a): `ask new
// --help` was exiting 2 with "ask new: flag: help requested" because the
// generic flag.Parse error handler did not distinguish flag.ErrHelp from
// real validation failures, and `ask init --help` had the same defect.
// The remaining subcommands routed to os.Stderr and auto-printed a bare
// "Usage of <name>:" block with exit 2 — present but not a proper help
// surface. After this fix all seven subcommands route help through
// printUsage on stdout with synopsis, brief, and flag table.
func TestSubcommandHelpExitsZero(t *testing.T) {
	cases := []struct {
		cmd          string
		wantSynopsis string
		wantFlag     string // a flag name expected in the flag table
	}{
		{"new", "ask new <title>", "-body"},
		{"init", "ask init [flags]", "-name"},
		{"list", "ask list [flags]", "-status"},
		{"show", "ask show <id-or-prefix>", "-json"},
		{"resolve", "ask resolve <id>", "-note"},
		{"reopen", "ask reopen <id>", "-reason"},
		{"close", "ask close <id>", "-reason"},
	}
	for _, tc := range cases {
		for _, flagForm := range []string{"--help", "-h"} {
			t.Run(tc.cmd+" "+flagForm, func(t *testing.T) {
				var code int
				out := captureStdout(t, func() {
					code = Run([]string{tc.cmd, flagForm})
				})
				if code != 0 {
					t.Fatalf("exit code: got %d, want 0; stdout=%q", code, out)
				}
				if !strings.Contains(out, "Usage: "+tc.wantSynopsis) {
					t.Errorf("stdout missing synopsis %q; got:\n%s", tc.wantSynopsis, out)
				}
				if !strings.Contains(out, tc.wantFlag) {
					t.Errorf("stdout missing flag %q; got:\n%s", tc.wantFlag, out)
				}
				if !strings.Contains(out, "Flags:") {
					t.Errorf("stdout missing Flags: header; got:\n%s", out)
				}
			})
		}
	}
}

// TestSubcommandUnknownFlagStillExitsTwo asserts that real flag-parse errors
// (unknown flags, bad values) keep their exit-2 contract: handleParseErr
// must distinguish flag.ErrHelp from other parse errors. Without this
// check a wrong refactor of usage.go could regress to exit-0 on every
// parse failure.
func TestSubcommandUnknownFlagStillExitsTwo(t *testing.T) {
	subcommands := []string{"new", "init", "list", "show", "resolve", "reopen", "close"}
	for _, cmd := range subcommands {
		t.Run(cmd, func(t *testing.T) {
			var code int
			stderr := captureStderr(t, func() {
				code = Run([]string{cmd, "--no-such-flag"})
			})
			if code != 2 {
				t.Fatalf("exit code: got %d, want 2; stderr=%q", code, stderr)
			}
			if !strings.Contains(stderr, "ask "+cmd+":") {
				t.Errorf("stderr missing %q prefix; got:\n%s", "ask "+cmd+":", stderr)
			}
		})
	}
}
