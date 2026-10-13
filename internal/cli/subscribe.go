// Package cli — `ask subscribe` and `ask unsubscribe` let a party other
// than an item's author say "when this item changes, run this handler for
// me". This file stores, reports and removes subscriptions; it never runs
// a handler.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aac/ask/internal/core"
)

// argFlags collects repeated `--arg key=value` flags in order.
type argFlags []string

func (a *argFlags) String() string     { return strings.Join(*a, " ") }
func (a *argFlags) Set(v string) error { *a = append(*a, v); return nil }

// subscribeJSONOutput is the `--json` shape of `ask subscribe`.
type subscribeJSONOutput struct {
	ID           string            `json:"id"`
	Status       core.Status       `json:"status"`
	Subscription core.Subscription `json:"subscription"`
}

// unsubscribeJSONOutput is the `--json` shape of `ask unsubscribe`.
type unsubscribeJSONOutput struct {
	ID           string            `json:"id"`
	Unsubscribed core.Subscription `json:"unsubscribed"`
}

// runSubscribe implements
//
//	ask subscribe <id> --on <event>[,<event>] --handler <name>
//	              [--arg key=value ...] [--json]
//
// Events are resolve, close and reopen; any is stored as all three. The
// handler is an executable at ~/.config/ask/callbacks/<name> on the host
// where the store lives.
//
// Exit codes, in this order: 2 for a closed item (nothing stored), a bad
// handler name, a bad argument, an unknown handler, or a subscription past
// the per-item cap; 8 when the item is already resolved and the
// subscription names resolve (stored anyway, since a reopen may follow);
// 6 when an identical subscription already exists (nothing added); 0
// otherwise. 3 / 4 / 5 as for every verb that addresses an item.
func runSubscribe(args []string) int {
	fs := flag.NewFlagSet("subscribe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	on := fs.String("on", "", "Events to subscribe to, comma-separated: resolve, close, reopen, or any")
	handler := fs.String("handler", "", "Handler name: an executable at ~/.config/ask/callbacks/<name>")
	var argList argFlags
	fs.Var(&argList, "arg", "Argument handed to the handler as data, key=value. Repeatable")
	asJSON := fs.Bool("json", false, "Emit {id, status, subscription} as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "subscribe",
			"ask subscribe <id> --on <event>[,<event>] --handler <name> [flags]",
			"Ask for a handler to run when this item is resolved, closed or reopened.\n"+
				"Stores the subscription and prints the item's current status; it never\n"+
				"runs the handler.")
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "ask subscribe: id required")
		return 2
	}
	if *on == "" {
		fmt.Fprintln(os.Stderr, "ask subscribe: --on is required (resolve, close, reopen or any)")
		return 2
	}
	events, err := core.ParseEvents(*on)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask subscribe: %v\n", err)
		return 2
	}
	if err := core.ValidateHandlerName(*handler); err != nil {
		fmt.Fprintf(os.Stderr, "ask subscribe: %v\n", err)
		return 2
	}
	subArgs, err := parseSubArgs(argList)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask subscribe: %v\n", err)
		return 2
	}
	if _, err := core.CheckHandler(*handler); err != nil {
		installed := core.InstalledHandlers()
		list := "none"
		if len(installed) > 0 {
			list = strings.Join(installed, ", ")
		}
		fmt.Fprintf(os.Stderr, "ask subscribe: %v; installed handlers: %s\n", err, list)
		return 2
	}

	store, code := openStoreCwd("subscribe")
	if code != 0 {
		return code
	}
	res, err := store.Subscribe(fs.Arg(0), events, *handler, subArgs, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask subscribe: %v\n", err)
		return mutateErrExit(err)
	}

	it, sub := res.Item, res.Subscription
	// An existing subscription was read from the subs file, a file on
	// disk: its id is kept to its line where it is printed as text.
	subID := oneLine(sub.ID)
	resolvedAlready := it.Status == core.StatusResolved && sub.Names(core.EventResolve)
	if res.Existing {
		fmt.Fprintf(os.Stderr, "ask subscribe: %s already has this subscription (%s); nothing was added\n", it.ID, subID)
	}
	if resolvedAlready {
		fmt.Fprintf(os.Stderr, "ask subscribe: %s is already resolved; the subscription is stored and fires if the item is reopened and resolved again\n", it.ID)
	}
	if *asJSON {
		emitJSON(subscribeJSONOutput{ID: it.ID, Status: it.Status, Subscription: sub})
	} else {
		state := "subscribed"
		if res.Existing {
			state = "already subscribed"
		}
		fmt.Printf("%s: %s %s (%s -> %s); status %s\n",
			it.ID, state, subID, strings.Join(sub.Events, ","), sub.Handler, it.Status)
	}
	switch {
	case resolvedAlready:
		return 8
	case res.Existing:
		return 6
	}
	return 0
}

// parseSubArgs turns the repeated --arg key=value flags into a map and
// checks them against core.ValidateSubArgs. A flag without `=` or a key
// given twice is refused.
func parseSubArgs(list []string) (map[string]string, error) {
	out := make(map[string]string, len(list))
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--arg %q: want key=value", kv)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--arg %s given twice", k)
		}
		out[k] = v
	}
	if err := core.ValidateSubArgs(out); err != nil {
		return nil, err
	}
	return out, nil
}

var subPrefixRe = regexp.MustCompile(`^[0-9a-f]{1,8}$`)

// runUnsubscribe implements `ask unsubscribe <id> <subscription-id-prefix>
// [--json]`. The subscription id is the 8-hex id `ask show` lists; any
// unambiguous prefix of it will do. The item's subs file is removed with
// its last subscription.
//
// Exit codes: 0 removed; 2 validation; 3 item or subscription not found;
// 4 ambiguous item or subscription prefix; 5 I/O.
func runUnsubscribe(args []string) int {
	fs := flag.NewFlagSet("unsubscribe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	asJSON := fs.Bool("json", false, "Emit {id, unsubscribed} as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "unsubscribe",
			"ask unsubscribe <id> <subscription-id-prefix> [flags]",
			"Remove one subscription from an item. `ask show <id>` lists the\n"+
				"subscription ids.")
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "ask unsubscribe: item id and subscription id required")
		return 2
	}
	subPrefix := fs.Arg(1)
	if !subPrefixRe.MatchString(subPrefix) {
		fmt.Fprintf(os.Stderr, "ask unsubscribe: invalid subscription id %q (want 1 to 8 lowercase hex characters)\n", subPrefix)
		return 2
	}
	store, code := openStoreCwd("unsubscribe")
	if code != 0 {
		return code
	}
	id, removed, err := store.Unsubscribe(fs.Arg(0), subPrefix)
	if err != nil {
		// The message can name stored subscription ids.
		fmt.Fprintf(os.Stderr, "ask unsubscribe: %s\n", cleanText(err.Error()))
		switch {
		case errors.Is(err, core.ErrSubNotFound):
			return 3
		case errors.Is(err, core.ErrSubAmbiguous):
			return 4
		}
		return mutateErrExit(err)
	}
	if *asJSON {
		emitJSON(unsubscribeJSONOutput{ID: id, Unsubscribed: *removed})
	} else {
		fmt.Printf("%s: unsubscribed %s\n", id, oneLine(removed.ID))
	}
	return 0
}

// formatSubscription is the one-line `ask show` rendering of a
// subscription: id, handler, events, arguments (sorted by key) and the
// RFC 3339 UTC time it was added.
func formatSubscription(s core.Subscription) string {
	keys := make([]string, 0, len(s.Args))
	for k := range s.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	// The subs file is read from disk and can have come from another
	// store, so what it holds is kept to its line (see oneLine).
	fmt.Fprintf(&b, "%s %s on %s", oneLine(s.ID), oneLine(s.Handler), oneLine(strings.Join(s.Events, ",")))
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%q", oneLine(k), s.Args[k])
	}
	fmt.Fprintf(&b, " added %s", s.AddedAt.UTC().Format(time.RFC3339))
	return b.String()
}
