// Package cli — `ask fires` reads back what the handlers of an item's
// subscriptions did. This file owns that verb and the words a fire's
// state is put in, which `ask show` shares.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aac/ask/internal/core"
)

// firesNow is the clock the ages of unfinished fires are measured
// against. Tests replace it.
var firesNow = time.Now

// fireRunningWindow is how long a fire with no result line is taken to be
// still running: the handler timeout plus the time the runner gives a
// killed handler to go away, with room to spare. Past it the runner is
// dead and the result line will never come.
const fireRunningWindow = 90 * time.Second

// runFires implements `ask fires [<id-or-prefix>] [--json]`. With an id,
// that item's fires, oldest first; without, every fire in the store. It
// only reads: nothing is created on disk, .ask/fires/ included.
//
// Exit codes: 0 (including no fires at all, which is `[]` under --json);
// 2 validation; 3 id not found; 4 ambiguous prefix; 5 I/O.
func runFires(args []string) int {
	fs := flag.NewFlagSet("fires", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	asJSON := fs.Bool("json", false, "Emit the fires as a JSON array on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "fires",
			"ask fires [<id-or-prefix>] [flags]",
			"Show what the handlers of an item's subscriptions did, one block per\n"+
				"fire, oldest first, with the output each handler printed. Without an\n"+
				"id, every fire in the store.")
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "ask fires: at most one id")
		return 2
	}

	store, code := openStoreCwd("fires")
	if code != 0 {
		return code
	}

	var ids []string
	if fs.NArg() == 1 {
		all, err := store.ListIDs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask fires: %v\n", err)
			return 5
		}
		full, err := core.ResolvePrefix(fs.Arg(0), all)
		if err != nil {
			switch {
			case errors.Is(err, core.ErrIDNotFound):
				fmt.Fprintf(os.Stderr, "ask fires: id %q not found\n", fs.Arg(0))
				return 3
			case errors.Is(err, core.ErrIDAmbiguous):
				fmt.Fprintf(os.Stderr, "ask fires: %v\n", err)
				return 4
			}
			fmt.Fprintf(os.Stderr, "ask fires: %v\n", err)
			return 5
		}
		ids = []string{full}
	} else {
		var err error
		if ids, err = store.FiredItemIDs(); err != nil {
			fmt.Fprintf(os.Stderr, "ask fires: %v\n", err)
			return 5
		}
	}

	fires := []core.Fire{}
	for _, id := range ids {
		got, skipped, err := store.LoadFires(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask fires: %v\n", err)
			return 5
		}
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "ask fires: %s: %d line(s) of the fire record are not fire records and were left out\n", id, skipped)
		}
		fires = append(fires, got...)
	}
	// Across items, oldest first by when each fire started; within one
	// item this keeps the order of the record.
	sort.SliceStable(fires, func(i, j int) bool { return fires[i].StartedAt.Before(fires[j].StartedAt) })

	if *asJSON {
		emitJSON(fires)
		return 0
	}
	now := firesNow()
	for i, f := range fires {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("fire %s  item %s  subscription %s  handler %s\n", f.Fire, f.Item, f.Sub, oneLine(f.Handler))
		fmt.Printf("  %s  %s  %s -> %s\n", f.At.UTC().Format(time.RFC3339), oneLine(f.Event), oneLine(string(f.From)), oneLine(string(f.To)))
		fmt.Printf("  %s\n", fireStateWords(f, now))
		if out := printable(f.Output); strings.TrimSpace(out) != "" {
			fmt.Println("  output:")
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
	return 0
}

// fireStateWords puts a fire's state in words:
//
//	ok, exit 0 in 0.4 s
//	failed, exit 22 in 7.2 s
//	not run: no handler named <name> on this machine
//	killed at the 60 s timeout
//	started, no result after <age> (it is running)
//	started, no result after <age> (it died; it is not retried)
//
// The durations and the age are the only relative figures. Nothing of the
// handler's output is in them.
func fireStateWords(f core.Fire, now time.Time) string {
	detail := ""
	if f.Detail != nil {
		detail = oneLine(*f.Detail)
	}
	took := ""
	if f.DurationMs != nil {
		took = fmt.Sprintf(" in %.1f s", float64(*f.DurationMs)/1000)
	}
	switch f.State {
	case core.FireOK:
		return "ok, exit 0" + took
	case core.FireFailed:
		if f.ExitCode != nil {
			return fmt.Sprintf("failed, exit %d%s", *f.ExitCode, took)
		}
		if detail == "" {
			detail = "no exit code"
		}
		return "failed, " + detail + took
	case core.FireNotRun:
		switch {
		case strings.HasPrefix(detail, "no handler named "):
			return "not run: " + detail + " on this machine"
		case detail == core.FireDetailNotRunnable:
			return fmt.Sprintf("not run: the handler %s is not an executable file on this machine", oneLine(f.Handler))
		case detail == "":
			detail = "no reason given"
		}
		return "not run: " + detail
	case core.FireSpawnFailed:
		if detail == "" {
			detail = "no reason given"
		}
		return "not run: could not be started: " + detail
	case core.FireTimedOut:
		return fmt.Sprintf("killed at the %d s timeout", int(core.FireTimeout/time.Second))
	case core.FireStateStarted:
		age := now.Sub(f.StartedAt)
		if age < 0 {
			age = 0
		}
		if age < fireRunningWindow {
			return fmt.Sprintf("started, no result after %s (it is running)", fireAge(age))
		}
		return fmt.Sprintf("started, no result after %s (it died; it is not retried)", fireAge(age))
	}
	return fmt.Sprintf("unknown state %q", oneLine(f.State))
}

// fireAge renders how long ago an unfinished fire started, in its largest
// whole unit: 12 s, 4 min, 3 h, 9 d.
func fireAge(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return fmt.Sprintf("%d s", int(d/time.Second))
	case d < 2*time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d d", int(d/(24*time.Hour)))
}

// oneLine makes a string read from a fire record safe to put inside a
// line of text: control characters become spaces and anything past 200
// bytes is cut. The record is a file on disk, and its detail can be an
// operating system's error message.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	const max = 200
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return strings.TrimSpace(s)
}

// printable is a handler's stored output as `ask fires` prints it: line
// breaks and tabs kept, every other control character replaced, so stored
// output cannot drive the terminal it is read on. --json carries the
// output as stored.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r) || r == utf8.RuneError:
			return ' '
		}
		return r
	}, s)
}
