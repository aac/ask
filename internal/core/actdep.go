package core

import "regexp"

// actIDRe recognizes a `blocks` ref shaped like an act issue id: `act-`
// followed by lowercase hex (act-3c89, act-58f80e). It is deliberately
// narrow. ask still stores every ref verbatim and never interprets it;
// this shape only decides whether `ask new` prints the act-side
// follow-up command, and a ref that matches is safe to paste into a
// shell as is. TestActIDRe_MatchesIDsTheActBinaryMints checks it against
// ids a real act binary mints.
var actIDRe = regexp.MustCompile(`^act-[0-9a-f]{4,32}$`)

// ActDepHintPrefix leads each follow-up line `ask new` writes to stderr
// (and `ask_new` adds as a content part). The command itself follows it
// verbatim, so a caller can take everything after the prefix.
const ActDepHintPrefix = "ask new: act ready ignores --blocks until the act issue carries the dep; run: "

// ActDepCommands returns, for each act-shaped ref in blocks, the command
// that registers the reciprocal block on the act side:
// `act dep add <act-id> --external <askID>`. Order follows blocks;
// a ref given twice yields one command. Refs that are not act ids yield
// nothing. See spec §1.6.
func ActDepCommands(askID string, blocks []string) []string {
	var out []string
	seen := make(map[string]bool, len(blocks))
	for _, ref := range blocks {
		if !actIDRe.MatchString(ref) || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, "act dep add "+ref+" --external "+askID)
	}
	return out
}
