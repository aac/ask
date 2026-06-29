// Package cli implements the `ask` command surface. This file owns the
// `ask show` verb: resolving an id-or-prefix and rendering one Item in
// either spec §1.5 JSON or a human-readable detail view.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aac/ask/internal/core"
)

// runShow implements `ask show <id-or-prefix>`. Spec §1.5: with --json,
// emits the single Item object; without --json, emits a human-readable
// detail block. The id argument may be a full id or any non-empty hex
// prefix; resolution is delegated to core.ResolvePrefix (spec §4.3).
//
// Exit codes (spec §2 and §4.3):
//   - 0 on success
//   - 2 on validation error (no id, flag parse failure)
//   - 3 on not found
//   - 4 on ambiguous prefix
//   - 5 on I/O error
func runShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	asJSON := fs.Bool("json", false, "emit JSON Item object (spec §1.5)")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "show",
			"ask show <id-or-prefix> [flags]",
			"Show one item's full detail. The id may be a full id or any non-empty prefix.")
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "ask show: id required")
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
		return 5
	}
	store, err := core.OpenStore(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
		return 5
	}

	ids, err := store.ListIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
		return 5
	}
	full, err := core.ResolvePrefix(fs.Arg(0), ids)
	if err != nil {
		switch {
		case errors.Is(err, core.ErrIDNotFound):
			fmt.Fprintf(os.Stderr, "ask show: id %q not found\n", fs.Arg(0))
			return 3
		case errors.Is(err, core.ErrIDAmbiguous):
			// core.ResolvePrefix wraps the matching ids in the error message
			// (comma-separated); spec §4.3 wants newline-separated on stderr.
			// Surface the raw error for now — it includes the ids in a
			// recognizable shape — and exit 4.
			fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
			return 4
		default:
			fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
			return 5
		}
	}

	it, err := store.Load(full)
	if err != nil {
		if errors.Is(err, core.ErrIDNotFound) {
			// Race: id was in the directory listing but the file is now
			// gone. Surface as not-found for the caller.
			fmt.Fprintf(os.Stderr, "ask show: id %q not found\n", full)
			return 3
		}
		fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
		return 5
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(it); err != nil {
			fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
			return 5
		}
		return 0
	}

	// Human-readable detail block. Optional fields (body, verifier,
	// verification_output, resolution_note, links, timestamps) are only
	// printed when set so the output is dense for the common case.
	fmt.Printf("ID:       %s\n", it.ID)
	fmt.Printf("Title:    %s\n", it.Title)
	fmt.Printf("Status:   %s\n", it.Status)
	fmt.Printf("Urgency:  %s\n", it.Urgency)
	if it.FiledBy != nil && *it.FiledBy != "" {
		fmt.Printf("FiledBy:  %s\n", *it.FiledBy)
	}
	if it.Recipient != nil && *it.Recipient != "" {
		fmt.Printf("To:       %s\n", *it.Recipient)
	}
	if it.TrackerRef != nil && *it.TrackerRef != "" {
		fmt.Printf("Tracker:  %s\n", *it.TrackerRef)
	}
	fmt.Printf("Created:  %s\n", it.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	if it.ResolvedAt != nil {
		fmt.Printf("Resolved: %s\n", it.ResolvedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if it.VerifiedAt != nil {
		fmt.Printf("Verified: %s\n", it.VerifiedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if it.ClosedAt != nil {
		fmt.Printf("Closed:   %s\n", it.ClosedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if it.Body != "" {
		fmt.Printf("\n%s\n", it.Body)
	}
	if it.Verifier != nil {
		fmt.Printf("\nVerifier: %s (%s, timeout=%ds)\n",
			it.Verifier.Command, it.Verifier.Type, it.Verifier.TimeoutSeconds)
	}
	if len(it.Links) > 0 {
		fmt.Println("\nLinks:")
		for _, l := range it.Links {
			fmt.Printf("  - %s: %s\n", l.Label, l.URL)
		}
	}
	if len(it.Blocks) > 0 {
		fmt.Println("\nBlocks:")
		for _, b := range it.Blocks {
			fmt.Printf("  - %s\n", b)
		}
	}
	if it.VerificationOutput != nil && *it.VerificationOutput != "" {
		fmt.Printf("\nLast verification output:\n%s\n", *it.VerificationOutput)
	}
	if it.ResolutionNote != nil && *it.ResolutionNote != "" {
		fmt.Printf("\nResolution note: %s\n", *it.ResolutionNote)
	}
	return 0
}
