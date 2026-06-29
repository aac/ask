// Package cli — `ask reopen` reverts a resolved item back to open. See
// transitions in internal/core for the state-machine semantics.
package cli

import (
	"flag"
	"io"
	"time"

	"github.com/aac/ask/internal/core"
)

// runReopen implements `ask reopen <id> [--reason <text>] [--json]`. Per
// spec §1.8 it reverts a resolved item to open; --reason populates
// verification_output as audit evidence (typically the verifier's failure
// output). A reopen on a closed item is invalid (exit 2); a reopen on an
// already-open item is an idempotent no-op (exit 6 with stderr warning,
// stdout still emits the success shape).
func runReopen(args []string) int {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	reason := fs.String("reason", "", "Captured verifier output or cause")
	asJSON := fs.Bool("json", false, "Emit the post-transition Item as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "reopen",
			"ask reopen <id> [flags]",
			"Reopen a resolved item (resolved → open). --reason captures the failure detail.")
	}
	return mutate("reopen", fs.Args(), *asJSON, func(it *core.Item) error {
		return core.Reopen(it, *reason, time.Now().UTC())
	})
}
