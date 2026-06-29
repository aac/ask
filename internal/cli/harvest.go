// Package cli — `ask harvest` surfaces asks filed in a worktree-dispatched
// subagent's .ask/ into the current store. See core.Harvest for the copy
// semantics; this file owns the flag plumbing, exit codes, and the
// human-vs-JSON output split.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aac/ask/internal/core"
)

// harvestJSONOutput is the structured §1-style summary emitted by
// `ask harvest --json`. Mirrors core.HarvestResult and adds the source
// path so a caller-side log captures both ends in one record.
type harvestJSONOutput struct {
	From      string   `json:"from"`
	Harvested []string `json:"harvested"`
	Cleaned   bool     `json:"cleaned"`
}

// runHarvest implements `ask harvest --from <path> [--clean] [--json]`.
// Both source and target must be initialized .ask/ stores. Pre-flight
// collision detection means the verb either fully succeeds or refuses
// without mutating either store. With --clean the source items are
// removed after a successful copy so re-runs are clean no-ops.
//
// Exit codes (spec §2):
//
//	0  copy succeeded (including the empty-source case)
//	2  validation error: --from missing, or source == target
//	5  I/O error, store-uninitialized, or id collision in target
//
// Idempotent-no-op exit 6 is not used here: an empty source is a real
// success, not a "already in target state" claim. Re-running after
// --clean is the same code path with zero items — also exit 0.
func runHarvest(args []string) int {
	fs := flag.NewFlagSet("harvest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	from := fs.String("from", "", "Source worktree path; must contain an initialized .ask/ directory")
	clean := fs.Bool("clean", false, "Remove source items after a successful copy")
	asJSON := fs.Bool("json", false, "Emit the harvest summary as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "harvest",
			"ask harvest --from <path> [flags]",
			"Copy items from a worktree's .ask/ into the current store. Used by orchestrators after a worktree subagent completes so the human's main inbox sees what the subagent filed.")
	}
	if strings.TrimSpace(*from) == "" {
		fmt.Fprintln(os.Stderr, "ask harvest: --from is required")
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask harvest: %v\n", err)
		return 5
	}
	target, err := core.OpenStore(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask harvest: %v\n", err)
		return 5
	}
	source, err := core.OpenStore(*from, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask harvest: source: %v\n", err)
		return 5
	}

	// Refuse to harvest a store into itself (EvalSymlinks-resolved equality).
	// Same store would mean every id collides with itself; better to catch
	// it as a misconfiguration than to surface as a thousand collisions.
	if sameStore(target.Root(), source.Root()) {
		fmt.Fprintln(os.Stderr, "ask harvest: source and target resolve to the same .ask/ store")
		return 2
	}

	res, err := core.Harvest(target, source, *clean)
	if err != nil {
		if errors.Is(err, core.ErrIDCollision) {
			fmt.Fprintf(os.Stderr, "ask harvest: %v\n", err)
			return 5
		}
		fmt.Fprintf(os.Stderr, "ask harvest: %v\n", err)
		return 5
	}

	if *asJSON {
		emitJSON(harvestJSONOutput{
			From:      *from,
			Harvested: res.Harvested,
			Cleaned:   res.Cleaned,
		})
	} else {
		if len(res.Harvested) == 0 {
			fmt.Printf("nothing to harvest from %s\n", *from)
		} else {
			fmt.Printf("harvested %d item(s) from %s: %s\n",
				len(res.Harvested), *from, strings.Join(res.Harvested, " "))
		}
	}
	return 0
}

// sameStore reports whether a and b resolve to the same path on disk
// (via filepath.EvalSymlinks). Used to refuse a self-harvest. Falls back
// to lexical equality when EvalSymlinks errors so the check is best-effort
// rather than failing the verb on a transient stat issue.
func sameStore(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}
