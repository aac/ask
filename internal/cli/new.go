package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aac/ask/internal/core"
)

// runNew implements `ask new <title> [flags]`. See docs/spec.md §1.6 for
// stdout shape, §2 for exit codes, and §1.1 for field validation rules.
func runNew(args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // route flag-parse errors via our own messaging
	fs.Usage = noUsage
	body := fs.String("body", "", "Detailed body")
	urgency := fs.String("urgency", "normal", "blocker|normal|fyi")
	verifier := fs.String("verifier", "", "Shell command to verify resolution")
	timeout := fs.Int("timeout", 0, "Verifier timeout in seconds (advisory)")
	tracker := fs.String("tracker-ref", "", "Backward provenance: the tracker item that motivated this ask (e.g. act-3c89); metadata only, no queue effect. Use --blocks for a blocking relationship")
	filedBy := fs.String("filed-by", "", "Free-form filer id")
	recipient := fs.String("recipient", "", "Free-form recipient ref (e.g. agent:data-prep, human:andrew, team:reviewers)")
	var blocks stringSliceFlag
	fs.Var(&blocks, "blocks", "Forward edge: a tracker item this ask blocks (e.g. act-3c89); consumed by act ready and ask list --blocks; repeatable")
	asJSON := fs.Bool("json", false, "Output the new item as JSON")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "new",
			"ask new <title> [flags]",
			"File a new request. Title is required (1..200 chars, no newlines).\n"+
				"\n"+
				"The schema also supports a `links` array ([{label, url}, ...]) on items,\n"+
				"rendered as clickable in UIs. ask new has no --links flag in v1; populate\n"+
				"links via the MCP ask_new tool or by writing the item JSON directly. See\n"+
				"`ask help schema` for the full field list.")
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "ask new: title is required")
		return 2
	}
	title := strings.TrimSpace(fs.Arg(0))
	if title == "" {
		fmt.Fprintln(os.Stderr, "ask new: title is required")
		return 2
	}
	if len(title) > 200 {
		fmt.Fprintln(os.Stderr, "ask new: title must be 1..200 characters")
		return 2
	}
	if strings.ContainsAny(title, "\n\r") {
		fmt.Fprintln(os.Stderr, "ask new: title must not contain newlines")
		return 2
	}

	u := core.Urgency(*urgency)
	if !u.Valid() {
		fmt.Fprintf(os.Stderr, "ask new: invalid urgency %q (want blocker|normal|fyi)\n", *urgency)
		return 2
	}

	// Validate each --blocks ref: non-empty after trim and within the
	// 1024-char per-ref cap. The trim guard rejects whitespace-only refs
	// (e.g. `--blocks " "`). ask never interprets the format; this is the
	// only validation. Empty []string means no blocks; that's fine.
	if msg, ok := validateBlocksRefs(blocks); !ok {
		fmt.Fprintf(os.Stderr, "ask new: %s\n", msg)
		return 2
	}

	// --recipient is validated when present: non-empty after trim and
	// within the 1024-char cap (the same shape as a single blocks ref).
	// Empty "" means no recipient — that's fine (omitted flag default).
	// ask never interprets the format; orchestrators do.
	if *recipient != "" {
		if msg, ok := validateRecipientRef(*recipient); !ok {
			fmt.Fprintf(os.Stderr, "ask new: %s\n", msg)
			return 2
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask new: %v\n", err)
		return 5
	}
	store, err := core.OpenStore(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask new: %v\n", err)
		return 5
	}

	now := time.Now().UTC()
	existingIDs, err := store.ListIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask new: %v\n", err)
		return 5
	}
	existsSet := make(map[string]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existsSet[id] = struct{}{}
	}
	exists := func(id string) bool { _, ok := existsSet[id]; return ok }

	id, err := core.NewID(store.Config().ProjectID, now, title, exists)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask new: %v\n", err)
		return 5
	}

	item := core.NewItem(id, title, u, core.StatusOpen, now)
	item.Body = *body
	if *filedBy != "" {
		v := *filedBy
		item.FiledBy = &v
	}
	if *tracker != "" {
		v := *tracker
		item.TrackerRef = &v
	}
	if *recipient != "" {
		v := *recipient
		item.Recipient = &v
	}
	if *verifier != "" {
		item.Verifier = &core.Verifier{
			Type:           core.VerifierShell,
			Command:        *verifier,
			TimeoutSeconds: *timeout,
		}
	}
	if len(blocks) > 0 {
		item.Blocks = append([]string{}, blocks...)
	}
	if err := store.Save(item); err != nil {
		fmt.Fprintf(os.Stderr, "ask new: %v\n", err)
		return 5
	}

	if *asJSON {
		emitJSON(item)
	} else {
		fmt.Println(item.ID)
	}
	return 0
}

// maxBlocksRefLen caps a single `blocks` ref. The field is opaque — ask
// never interprets the format — so we want a sane upper bound that
// permits long synthetic ids and short URLs without inviting body-sized
// payloads. 1024 matches the convention sketched in act-2a1b.
const maxBlocksRefLen = 1024

// maxRecipientRefLen caps a `recipient` ref. Same bound as a single
// blocks ref — both are opaque cross-system strings ask never
// interprets, and 1024 is comfortably above any sensible identifier
// scheme (`agent:<name>`, `human:<handle>`, `team:<id>`, URLs, ULIDs).
const maxRecipientRefLen = 1024

// validateRecipientRef enforces the rules for a single `--recipient`
// value: non-empty after a whitespace trim and within the per-ref
// length cap. Mirrors validateBlocksRefs (single ref shape). ask never
// interprets the format; this is the only validation. Returns
// (errorMessage, false) on the first failure, ("", true) on success.
// The error string is shaped to be wrapped with the verb prefix at the
// caller (e.g. `ask new: ...`).
func validateRecipientRef(ref string) (string, bool) {
	if strings.TrimSpace(ref) == "" {
		return "recipient: ref must not be empty or whitespace-only", false
	}
	if len(ref) > maxRecipientRefLen {
		return fmt.Sprintf("recipient: ref exceeds %d characters", maxRecipientRefLen), false
	}
	return "", true
}

// validateBlocksRefs enforces the per-ref rules for the `blocks` slice:
// each ref must be non-empty after a whitespace trim (rejects ""s and
// whitespace-only refs) and must not exceed maxBlocksRefLen characters.
// Returns (errorMessage, false) on the first failure, ("", true) on
// success. The error string is shaped to be wrapped with the verb prefix
// at the caller (e.g. `ask new: ...`).
func validateBlocksRefs(refs []string) (string, bool) {
	for _, r := range refs {
		if strings.TrimSpace(r) == "" {
			return "blocks: ref must not be empty or whitespace-only", false
		}
		if len(r) > maxBlocksRefLen {
			return fmt.Sprintf("blocks: ref exceeds %d characters", maxBlocksRefLen), false
		}
	}
	return "", true
}
