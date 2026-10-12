package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TextUpdate is an in-place correction to an item's title and/or body —
// the edit behind `ask update` and the `ask_update` MCP tool. Both
// surfaces build one and call Apply, so the rules (title trimmed, body
// replaced or appended, never both) live in exactly one place. A nil
// field is "leave alone"; a non-nil pointer to "" is an explicit value
// (Body: clear the body).
//
// Callers reject Body and BodyAppend being set together before calling
// Apply; if both arrive anyway, Body wins.
type TextUpdate struct {
	Title      *string
	Body       *string
	BodyAppend *string
}

// Empty reports whether the update names no field at all.
func (u TextUpdate) Empty() bool {
	return u.Title == nil && u.Body == nil && u.BodyAppend == nil
}

// Apply edits it in memory and reports whether anything changed. It makes
// no state transition and stamps no timestamps. A false return is the
// idempotent no-op: the item already reads exactly as requested and the
// caller should skip the write.
func (u TextUpdate) Apply(it *Item) bool {
	prevTitle, prevBody := it.Title, it.Body
	if u.Title != nil {
		it.Title = strings.TrimSpace(*u.Title)
	}
	switch {
	case u.Body != nil:
		it.Body = *u.Body
	case u.BodyAppend != nil:
		it.Body = AppendBody(it.Body, *u.BodyAppend)
	}
	return it.Title != prevTitle || it.Body != prevBody
}

// AppendBody joins an existing body and an appended note with a blank
// line, per `act update --description-append`. An empty existing body
// yields the addition alone rather than a leading blank line.
func AppendBody(existing, addition string) string {
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + addition
}

// ValidateTitle enforces the spec §1.1 title rules: non-empty after trim,
// 1..200 characters, no newlines. Characters are Unicode code points, not
// bytes, so the limit is the one the MCP schemas advertise as maxLength
// and a 200-character title in any script is accepted. Returns (errorMessage, false) on the
// first failure; the message is shaped to be wrapped with the verb prefix
// at the caller.
func ValidateTitle(title string) (string, bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return "title must not be empty", false
	}
	if utf8.RuneCountInString(t) > 200 {
		return "title must be 1..200 characters", false
	}
	if strings.ContainsAny(t, "\n\r") {
		return "title must not contain newlines", false
	}
	return "", true
}

// MaxRefLen caps a `recipient` value and each `blocks` ref (spec §1.1).
// Both are opaque cross-system strings ask never interprets; 1024 is
// comfortably above any sensible identifier scheme (`act-XXXX`,
// `agent:<name>`, URLs, ULIDs) without inviting body-sized payloads.
const MaxRefLen = 1024

// ValidateRef enforces the one rule set a `recipient` value and a single
// `blocks` ref share: non-empty after a whitespace trim, and at most
// MaxRefLen characters. Characters are Unicode code points, not bytes,
// as in ValidateTitle and as the MCP schemas' maxLength reads. field is
// the name the message leads with ("recipient" or "blocks"). Returns
// (errorMessage, false) on the first failure; the message is shaped to
// be wrapped with the verb prefix at the caller.
func ValidateRef(field, ref string) (string, bool) {
	if strings.TrimSpace(ref) == "" {
		return field + ": ref must not be empty or whitespace-only", false
	}
	if utf8.RuneCountInString(ref) > MaxRefLen {
		return fmt.Sprintf("%s: ref exceeds %d characters", field, MaxRefLen), false
	}
	return "", true
}
