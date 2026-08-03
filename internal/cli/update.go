// Package cli — `ask update` corrects an item's text in place. It is the
// only verb that edits an existing item's title or body, and it makes no
// state transition: an ask that still needs a human stays open while its
// wording is fixed.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aac/ask/internal/core"
)

// runUpdate implements
//
//	ask update <id> [--title <text>]
//	               [--body <text> | --body-file <path|-> |
//	                --body-append <text> | --body-append-file <path|->]
//	               [--json]
//
// The flag names mirror `act update` (--description / --description-append
// / --description-file) so the sibling tools stay learnable together;
// ask's field is `body`, so the names follow the field.
//
// --body-append is the load-bearing half. Correcting a stale ask is
// almost always "add a dated status correction", not "rewrite what was
// true when it was filed" — and doing that by hand previously meant
// editing .ask/items/<id>.json directly or resolve-and-refiling, the
// latter of which closes an ask that still needs the human (act-82bf7a).
//
// Exit codes per spec §2: 0 success, 2 validation, 3 not-found, 4
// ambiguous, 5 I/O, 6 no-op (the requested text is already what's on
// disk), 7 stranded store.
func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	title := fs.String("title", "", "Replace the title (1..200 chars, no newlines)")
	body := fs.String("body", "", "Replace the body. An explicit empty string clears it")
	bodyFile := fs.String("body-file", "", "Replace the body with the contents of this file (UTF-8); use - for stdin")
	bodyAppend := fs.String("body-append", "", "Append this text to the existing body, separated by a blank line. The note-append path: annotate an ask without a read-modify-write of the whole body")
	bodyAppendFile := fs.String("body-append-file", "", "Append the contents of this file to the existing body (UTF-8); use - for stdin")
	asJSON := fs.Bool("json", false, "Emit the updated Item as JSON on stdout")
	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		return handleParseErr(err, fs, "update",
			"ask update <id> [flags]",
			"Correct an item's title or body in place. Makes no state transition:\n"+
				"an open ask stays open. Use --body-append to add a dated status\n"+
				"correction rather than rewriting what was true when it was filed.")
	}

	// Distinguish "flag absent" from "flag set to empty string": `--body ""`
	// is an explicit clear, while an omitted --body must not blank the body.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	bodyModes := []string{}
	for _, name := range []string{"body", "body-file", "body-append", "body-append-file"} {
		if set[name] {
			bodyModes = append(bodyModes, "--"+name)
		}
	}
	if len(bodyModes) > 1 {
		fmt.Fprintf(os.Stderr, "ask update: %s are mutually exclusive\n", strings.Join(bodyModes, ", "))
		return 2
	}
	if !set["title"] && len(bodyModes) == 0 {
		fmt.Fprintln(os.Stderr, "ask update: nothing to update (want --title, --body, --body-file, --body-append, or --body-append-file)")
		return 2
	}

	if set["title"] {
		if msg, ok := validateTitle(*title); !ok {
			fmt.Fprintf(os.Stderr, "ask update: %s\n", msg)
			return 2
		}
	}

	// Read file/stdin sources before touching the store so an unreadable
	// path fails without having opened anything.
	newBody, appendBody := *body, *bodyAppend
	switch {
	case set["body-file"]:
		text, err := readTextSource(*bodyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
			return 5
		}
		newBody = text
	case set["body-append-file"]:
		text, err := readTextSource(*bodyAppendFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
			return 5
		}
		appendBody = text
	}

	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "ask update: id required")
		return 2
	}
	store, code := openStoreCwd("update")
	if code != 0 {
		return code
	}
	ids, err := store.ListIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
		return 5
	}
	full, err := core.ResolvePrefix(fs.Arg(0), ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
		if errors.Is(err, core.ErrIDNotFound) {
			return 3
		}
		return 5
	}

	prevTitle, prevBody := it.Title, it.Body
	if set["title"] {
		it.Title = strings.TrimSpace(*title)
	}
	switch {
	case set["body"], set["body-file"]:
		it.Body = newBody
	case set["body-append"], set["body-append-file"]:
		it.Body = appendBodyText(it.Body, appendBody)
	}

	// No-op envelope (spec §1.8 + §2): the item already reads exactly as
	// requested. Skip the write so a non-mutation doesn't touch mtime, and
	// return 6 so scripts that care can branch; stdout still emits the
	// success shape.
	if it.Title == prevTitle && it.Body == prevBody {
		fmt.Fprintf(os.Stderr, "ask update: %s already reads as requested\n", it.ID)
		emitUpdated(it, *asJSON, "unchanged")
		return 6
	}

	if err := store.Save(it); err != nil {
		fmt.Fprintf(os.Stderr, "ask update: %v\n", err)
		return 5
	}
	emitUpdated(it, *asJSON, "updated")
	return 0
}

// emitUpdated writes the success shape: the full Item under --json, or
// `<id>: <state>` as plain text.
func emitUpdated(it *core.Item, asJSON bool, state string) {
	if asJSON {
		emitJSON(it)
		return
	}
	fmt.Printf("%s: %s\n", it.ID, state)
}

// appendBodyText joins an existing body and an appended note with a blank
// line, per `act update --description-append`. An empty existing body
// yields the addition alone rather than a leading blank line.
func appendBodyText(existing, addition string) string {
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + addition
}

// readTextSource reads path as UTF-8 text, treating "-" as stdin. Mirrors
// `act update --description-file`.
func readTextSource(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// validateTitle enforces the spec §1.1 title rules shared by `ask new` and
// `ask update`: non-empty after trim, 1..200 characters, no newlines.
// Returns (errorMessage, false) on the first failure; the message is
// shaped to be wrapped with the verb prefix at the caller.
func validateTitle(title string) (string, bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return "title must not be empty", false
	}
	if len(t) > 200 {
		return "title must be 1..200 characters", false
	}
	if strings.ContainsAny(t, "\n\r") {
		return "title must not contain newlines", false
	}
	return "", true
}
