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
	"sort"
	"strings"
	"time"

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

	store, code := openStoreCwd("show")
	if code != 0 {
		return code
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

	// Subscriptions and their fires are read only for the text view:
	// `ask show --json` is the Item and nothing else.
	var subs *core.SubsFile
	var fires []core.Fire
	var firesErr error
	if !*asJSON {
		if subs, err = store.LoadSubs(full); err != nil {
			fmt.Fprintf(os.Stderr, "ask show: %v\n", err)
			return 5
		}
		// An unreadable fire record does not hide the item: the section
		// below says so in place of the lines it cannot give.
		if len(subs.Subscriptions) > 0 {
			fires, _, firesErr = store.LoadFires(full)
		}
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
	if len(subs.Subscriptions) > 0 {
		fmt.Println("\nSubscriptions:")
		for _, sub := range subs.Subscriptions {
			fmt.Printf("  - %s\n", formatSubscription(sub))
			if firesErr != nil {
				fmt.Printf("      fire record unreadable: %s\n", oneLine(firesErr.Error()))
				continue
			}
			for _, line := range subscriptionFireLines(it, sub, fires, firesNow()) {
				fmt.Printf("      %s\n", line)
			}
		}
	}
	return 0
}

// showFireLines is how many of a subscription's fires `ask show` lists:
// the latest ones. `ask fires` has them all.
const showFireLines = 3

// subscriptionFireLines is what `ask show` says under one subscription
// about its fires: one line for each of the latest showFireLines, oldest
// first, in the words of fireStateWords,
//
//	<time>  <event>   ok, exit 0 in 0.4 s
//
// or, when there is nothing of the kind to say, one line that says why:
//
//	not fired yet: no <events> since this was added
//	missed: <event> at <time> by a build that does not fire; not retried
//
// A fire was missed when the item carries a resolve or close time later
// than the subscription was added, the subscription names that event, and
// the record holds no fire of it for that time: whatever made the change
// did not fire. A missed reopen leaves no such trace, since a reopen
// stamps no time on the item.
//
// Every time is RFC 3339 UTC. Handler output is never among the lines.
func subscriptionFireLines(it *core.Item, sub core.Subscription, fires []core.Fire, now time.Time) []string {
	type entry struct {
		at   time.Time
		line string
	}
	var entries []entry
	var mine []core.Fire
	for _, f := range fires {
		if f.Sub != sub.ID {
			continue
		}
		mine = append(mine, f)
		entries = append(entries, entry{f.At, fmt.Sprintf("%s  %-7s   %s",
			f.At.UTC().Format(time.RFC3339), oneLine(f.Event), fireStateWords(f, now))})
	}
	for _, change := range []struct {
		event string
		at    *time.Time
	}{{core.EventResolve, it.ResolvedAt}, {core.EventClose, it.ClosedAt}} {
		if change.at == nil || !change.at.After(sub.AddedAt) || !sub.Names(change.event) {
			continue
		}
		fired := false
		for _, f := range mine {
			if f.Event == change.event && f.At.Equal(*change.at) {
				fired = true
				break
			}
		}
		if !fired {
			entries = append(entries, entry{*change.at, fmt.Sprintf("missed: %s at %s by a build that does not fire; not retried",
				change.event, change.at.UTC().Format(time.RFC3339))})
		}
	}
	if len(entries) == 0 {
		return []string{fmt.Sprintf("not fired yet: no %s since this was added", orList(sub.Events))}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	if len(entries) > showFireLines {
		entries = entries[len(entries)-showFireLines:]
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.line
	}
	return lines
}

// orList joins words as "a", "a or b", "a, b or c".
func orList(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}
