// Package cli — shared helpers for subcommand flag parsing. This file
// owns the -h/--help discipline: every subcommand suppresses the flag
// package's auto-printed usage (so we control output formatting) and
// routes a help request through printUsage on stdout with a synopsis
// and brief description.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// noUsage is the fs.Usage no-op installed by every subcommand so the flag
// package never auto-prints its bare "Usage of <name>:" block on either a
// help request or a parse error. handleParseErr renders our own block
// instead.
var noUsage = func() {}

// handleParseErr converts a non-nil error from fs.Parse into a subcommand
// exit code and the appropriate output. On flag.ErrHelp it prints the
// command-specific usage block to stdout and returns 0. On any other parse
// error it prints "ask <cmd>: <err>" plus a pointer to --help on stderr
// and returns 2 (spec §2 validation-error exit code).
//
// Wiring per subcommand, before fs.Parse:
//
//	fs.SetOutput(io.Discard)
//	fs.Usage = noUsage
//
// then on a non-nil parse error:
//
//	return handleParseErr(err, fs, "<cmd>", "<synopsis>", "<brief>")
func handleParseErr(err error, fs *flag.FlagSet, cmd, synopsis, brief string) int {
	if errors.Is(err, flag.ErrHelp) {
		printUsage(fs, synopsis, brief)
		return 0
	}
	fmt.Fprintf(os.Stderr, "ask %s: %v\n", cmd, err)
	fmt.Fprintf(os.Stderr, "Run `ask %s --help` for usage.\n", cmd)
	return 2
}

// printUsage writes a command-specific help block to stdout: a "Usage:"
// line with the synopsis (including positional args), a blank line, the
// one-line brief, a "Flags:" header, and the auto-generated flag table
// from fs.PrintDefaults.
func printUsage(fs *flag.FlagSet, synopsis, brief string) {
	out := os.Stdout
	fmt.Fprintf(out, "Usage: %s\n", synopsis)
	if brief != "" {
		fmt.Fprintf(out, "\n%s\n", brief)
	}
	fmt.Fprintln(out, "\nFlags:")
	fs.SetOutput(out)
	fs.PrintDefaults()
}
