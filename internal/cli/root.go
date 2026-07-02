package cli

import (
	"fmt"
	"os"
)

type commandFunc func(args []string) int

var commands = map[string]commandFunc{
	"init":    runInit,
	"new":     runNew,
	"list":    runList,
	"show":    runShow,
	"resolve": runResolve,
	"reopen":  runReopen,
	"close":   runClose,
	"harvest": runHarvest,
	"mcp":     runMCP,
	"help":    runHelp,
	"version": runVersion,
	"-h":      runHelp,
	"--help":  runHelp,
}

// Run dispatches the top-level subcommand. Returns the exit code per the
// taxonomy in docs/spec.md §2.
func Run(args []string) int {
	if len(args) == 0 {
		return runHelp(nil)
	}
	cmd, rest := args[0], args[1:]
	fn, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "ask: unknown command %q (run `ask help`)\n", cmd)
		return 2
	}
	return fn(rest)
}
