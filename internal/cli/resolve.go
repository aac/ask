// Package cli — `ask resolve` mutates an open item to resolved. See
// transitions in internal/core for the state-machine semantics.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aac/ask/internal/core"
)

// runResolve implements `ask resolve <id> [--note <text>] [--json]`. Per
// spec §1.8 it transitions an open item to resolved; --note populates
// resolution_note when non-empty. Per spec §2 a resolve on a closed item is
// a validation error (exit 2); a resolve on an already-resolved item is an
// idempotent no-op (exit 6 with stderr warning, stdout still emits the
// success shape) per the exit-6 envelope.
func runResolve(args []string) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	note := fs.String("note", "", "Optional context recorded as resolution_note")
	asJSON := fs.Bool("json", false, "Emit the post-transition Item as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "resolve",
			"ask resolve <id> [flags]",
			"Mark an item resolved (open → resolved). Typically called after a human completes the request.")
	}
	return mutate("resolve", fs.Args(), *asJSON, func(it *core.Item, now time.Time) error {
		return core.Resolve(it, *note, now)
	})
}

// reorderFlagsFirst lets users invoke commands in either order:
//
//	ask resolve <id> --note "x"
//	ask resolve --note "x" <id>
//
// Go's stdlib flag package stops parsing flags at the first positional, so
// to support the brief's example syntax (`ask resolve ask-3c89 --note ...`)
// we shuffle flag args (anything starting with `-` plus their value when a
// `--flag=value` form isn't used) to the front. Stops at a literal `--`,
// which is preserved verbatim along with everything after it.
func reorderFlagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Treat `--` and the rest as positional; flag.Parse handles it.
			positional = append(positional, args[i:]...)
			break
		}
		if len(a) >= 2 && a[0] == '-' {
			flags = append(flags, a)
			// If the flag isn't in `--name=value` form and a value-bearing
			// token follows (i.e. not another flag), greedily consume it.
			// This isn't perfect for boolean flags whose next token starts
			// with `-` (rare in our surface), but it covers --note/--reason
			// cleanly. A bare `-` is the conventional stdin sentinel
			// (`--body-file -`), never a flag, so it counts as a value.
			if !strings.Contains(a, "=") && i+1 < len(args) && (len(args[i+1]) == 0 || args[i+1] == "-" || args[i+1][0] != '-') {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// mutate is the shared helper for resolve/reopen/close. It requires a
// single id arg, opens the on-disk store, and hands the transition to
// core.Mutate, which resolves the id prefix, loads the item, runs fn and
// saves, all under the store lock. It then emits the success shape on
// stdout (id+arrow, or full Item when asJSON). When the transition was a
// no-op (the item already in the target state), it instead writes a stderr
// warning per spec §1.8 + §2 and returns exit 6; stdout still emits the
// success shape so scripts piping stdout keep working.
//
// Exit codes per spec §2: 0 success, 2 validation, 3 not-found, 4
// ambiguous, 5 I/O (including a store-lock timeout), 6 idempotent no-op.
func mutate(verb string, args []string, asJSON bool, fn func(*core.Item, time.Time) error) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "ask %s: id required\n", verb)
		return 2
	}
	store, code := openStoreCwd(verb)
	if code != 0 {
		return code
	}
	ch, err := store.Mutate(args[0], time.Now().UTC(), fn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return mutateErrExit(err)
	}
	it := ch.Item
	if !ch.Changed {
		fmt.Fprintf(os.Stderr, "ask %s: already %s\n", verb, it.Status)
	}
	if asJSON {
		emitJSON(it)
	} else if !ch.Changed {
		fmt.Printf("%s: already %s\n", it.ID, it.Status)
	} else {
		fmt.Printf("%s: %s -> %s\n", it.ID, ch.From, it.Status)
	}
	if !ch.Changed {
		return 6
	}
	return 0
}

// mutateErrExit maps a core.Mutate failure to its spec §2 exit code: 2 when
// the change was refused, 3 / 4 when the id did not resolve, 5 for I/O and
// for a store-lock timeout.
func mutateErrExit(err error) int {
	var refused *core.RefusedError
	switch {
	case errors.As(err, &refused):
		return 2
	case errors.Is(err, core.ErrIDNotFound):
		return 3
	case errors.Is(err, core.ErrIDAmbiguous):
		return 4
	}
	return 5
}
