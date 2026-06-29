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
	return mutate("resolve", fs.Args(), *asJSON, func(it *core.Item) error {
		return core.Resolve(it, *note, time.Now().UTC())
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
			// cleanly.
			if !strings.Contains(a, "=") && i+1 < len(args) && (len(args[i+1]) == 0 || args[i+1][0] != '-') {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// mutate is the shared helper for resolve/reopen/close. It (1) requires a
// single id arg, (2) opens the on-disk store, (3) resolves the id prefix to
// a full id, (4) loads the item, (5) runs fn (the transition), (6) saves,
// (7) emits the success shape on stdout (id+arrow, or full Item when
// asJSON). When the transition was a no-op (it.Status unchanged), it
// instead writes a stderr warning per spec §1.8 + §2 and returns exit 6;
// stdout still emits the success shape so scripts piping stdout keep
// working.
//
// Exit codes per spec §2: 0 success, 2 validation, 3 not-found, 4
// ambiguous, 5 I/O, 6 idempotent no-op.
func mutate(verb string, args []string, asJSON bool, fn func(*core.Item) error) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "ask %s: id required\n", verb)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return 5
	}
	store, err := core.OpenStore(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return 5
	}
	ids, err := store.ListIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return 5
	}
	full, err := core.ResolvePrefix(args[0], ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		switch {
		case errors.Is(err, core.ErrIDNotFound):
			return 3
		case errors.Is(err, core.ErrIDAmbiguous):
			return 4
		}
		return 5
	}
	it, err := store.Load(full)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		if errors.Is(err, core.ErrIDNotFound) {
			return 3
		}
		return 5
	}
	prev := it.Status
	if err := fn(it); err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return 2
	}
	// Detect idempotent no-op: the core transitions return nil and leave
	// it.Status unchanged when the item is already in the target state
	// (resolve→resolved, reopen→open, close→closed). We never re-Save in
	// the no-op path: nothing changed, and skipping the write avoids
	// touching the file mtime for a non-mutation.
	noop := it.Status == prev
	if !noop {
		if err := store.Save(it); err != nil {
			fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
			return 5
		}
	}

	if noop {
		fmt.Fprintf(os.Stderr, "ask %s: already %s\n", verb, it.Status)
	}
	if asJSON {
		emitJSON(it)
	} else if noop {
		fmt.Printf("%s: already %s\n", it.ID, it.Status)
	} else {
		fmt.Printf("%s: %s -> %s\n", it.ID, prev, it.Status)
	}
	if noop {
		return 6
	}
	return 0
}
