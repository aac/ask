// Package cli implements the `ask` command surface. This file owns the
// `ask list` verb: filtering, sorting, and rendering items in either the
// spec §3.3 tab-separated plain-text form or the §1.4 JSON array form.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aac/ask/internal/core"
)

// stringSliceFlag is a flag.Value that accumulates repeated occurrences of
// the same flag (e.g. `--status=open --status=resolved`). Spec §3.4 requires
// `--status` and `--urgency` to be repeatable with OR semantics.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

// runList implements `ask list`. Default filter: hide closed items
// (spec §3.1). Sort order is delegated to core.FileStore.List which
// implements spec §3.2 (urgency desc, then created_at asc, then id asc).
//
// Exit codes (spec §2):
//   - 0 on success (including empty result set)
//   - 2 on validation error (bad flag, bad enum value, --status + --all)
//   - 5 on I/O error (cannot open store, cannot read items dir)
func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	var statuses stringSliceFlag
	var urgencies stringSliceFlag
	var blocks stringSliceFlag
	var recipients stringSliceFlag
	fs.Var(&statuses, "status", "filter by status (open|resolved|closed); repeatable")
	fs.Var(&urgencies, "urgency", "filter by urgency (blocker|normal|fyi); repeatable")
	fs.Var(&blocks, "blocks", "filter to items whose blocks array contains this ref; repeatable (OR)")
	fs.Var(&recipients, "recipient", "filter to items whose recipient equals this ref; repeatable (OR)")
	all := fs.Bool("all", false, "list items in every state (equivalent to no status filter)")
	asJSON := fs.Bool("json", false, "emit JSON array (spec §1.4)")
	if err := fs.Parse(args); err != nil {
		return handleParseErr(err, fs, "list",
			"ask list [flags]",
			"List items in this project. Defaults to hiding closed items.")
	}

	// Spec §3.4: --all is mutually exclusive with --status. We treat any
	// explicit --status as overriding the default filter, so --all is only
	// meaningful when no --status was given.
	if *all && len(statuses) > 0 {
		fmt.Fprintln(os.Stderr, "ask list: --all and --status are mutually exclusive")
		return 2
	}

	// Validate enum values up front so the user gets a clear error rather
	// than a silent empty result.
	for _, s := range statuses {
		if !core.Status(s).Valid() {
			fmt.Fprintf(os.Stderr, "ask list: invalid status %q (want one of open, resolved, closed)\n", s)
			return 2
		}
	}
	for _, u := range urgencies {
		if !core.Urgency(u).Valid() {
			fmt.Fprintf(os.Stderr, "ask list: invalid urgency %q (want one of blocker, normal, fyi)\n", u)
			return 2
		}
	}
	// Reuse the same emptiness/length rules as `ask new --blocks`. A
	// filter ref that violates them would silently match nothing; surface
	// the validation error instead.
	if msg, ok := validateBlocksRefs(blocks); !ok {
		fmt.Fprintf(os.Stderr, "ask list: %s\n", msg)
		return 2
	}
	// --recipient filter validated with the same shape as --blocks (a
	// silent-miss would be confusing if the operator passes whitespace).
	for _, r := range recipients {
		if msg, ok := validateRecipientRef(r); !ok {
			fmt.Fprintf(os.Stderr, "ask list: %s\n", msg)
			return 2
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask list: %v\n", err)
		return 5
	}
	store, err := core.OpenStore(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask list: %v\n", err)
		return 5
	}
	items, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask list: %v\n", err)
		return 5
	}

	filtered := make([]*core.Item, 0, len(items))
	for _, it := range items {
		if !statusMatches(it.Status, statuses, *all) {
			continue
		}
		if !urgencyMatches(it.Urgency, urgencies) {
			continue
		}
		if !blocksMatches(it.Blocks, blocks) {
			continue
		}
		if !recipientMatches(it.Recipient, recipients) {
			continue
		}
		filtered = append(filtered, it)
	}

	if *asJSON {
		// Spec §1.4: empty array (never null) when nothing matches.
		if filtered == nil {
			filtered = []*core.Item{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(filtered); err != nil {
			fmt.Fprintf(os.Stderr, "ask list: %v\n", err)
			return 5
		}
		return 0
	}

	// Spec §3.3: tab-separated columns, no header row.
	//   <id>\t<urgency>\t<status>\t<verifier-marker>\t<title>
	// urgency left-padded to 7, status left-padded to 8, verifier-marker is
	// "V" if a verifier is attached, single space otherwise.
	for _, it := range filtered {
		marker := " "
		if it.Verifier != nil {
			marker = "V"
		}
		fmt.Printf("%s\t%-7s\t%-8s\t%s\t%s\n",
			it.ID,
			string(it.Urgency),
			string(it.Status),
			marker,
			it.Title,
		)
	}
	return 0
}

// statusMatches applies the spec §3.1/§3.4 status filter. Precedence:
//   - explicit --status list: include only items whose status is in the list
//     (OR semantics across repeated flags).
//   - --all: include every status.
//   - default (neither flag): include open and resolved; exclude closed.
func statusMatches(s core.Status, want []string, all bool) bool {
	if len(want) > 0 {
		for _, w := range want {
			if string(s) == w {
				return true
			}
		}
		return false
	}
	if all {
		return true
	}
	return s != core.StatusClosed
}

// urgencyMatches applies the spec §3.4 urgency filter. No filter means
// include every urgency; otherwise OR semantics across repeated flags.
func urgencyMatches(u core.Urgency, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if string(u) == w {
			return true
		}
	}
	return false
}

// blocksMatches applies the `--blocks` filter. An item matches when its
// Blocks array contains at least one of the requested refs (exact
// match; OR semantics across repeated flags, mirroring --status /
// --urgency). No filter means include every item.
func blocksMatches(have []string, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

// recipientMatches applies the `--recipient` filter. An item matches
// when its Recipient field equals one of the requested refs (exact
// match; OR semantics across repeated flags). No filter means include
// every item. Items without a recipient set (Recipient == nil) only
// match when no filter is applied — there is no sentinel for "the
// implicit human recipient" in v1; orchestrators that want to filter
// for the implicit case omit --recipient and post-filter the result
// themselves.
func recipientMatches(have *string, want []string) bool {
	if len(want) == 0 {
		return true
	}
	if have == nil {
		return false
	}
	for _, w := range want {
		if *have == w {
			return true
		}
	}
	return false
}
