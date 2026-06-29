// Package cli — `ask close` terminates an item's lifecycle. See transitions
// in internal/core for the state-machine semantics.
package cli

import (
	"flag"
	"io"
	"time"

	"github.com/aac/ask/internal/core"
)

// runClose implements `ask close <id> [--reason <text>] [--json]`. Per
// spec §1.8 it transitions an item to closed from open (cancel/dismiss
// path) or from resolved (normal close). --reason populates resolution_note
// when non-empty. Close on an already-closed item is an idempotent no-op
// (exit 6 with stderr warning, stdout still emits the success shape).
func runClose(args []string) int {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	reason := fs.String("reason", "", "Optional context (cancel/dismiss or no-verifier close)")
	asJSON := fs.Bool("json", false, "Emit the post-transition Item as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "close",
			"ask close <id> [flags]",
			"Close an item. From resolved this is \"verified, done\"; from open this cancels/dismisses.")
	}
	return mutate("close", fs.Args(), *asJSON, func(it *core.Item) error {
		return core.Close(it, *reason, time.Now().UTC())
	})
}
