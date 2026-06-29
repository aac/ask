package core

import (
	"errors"
	"time"
)

// ErrInvalidTransition is returned for state changes the matrix forbids.
// Per the brief: Resolve and Reopen of a closed item are errors; the
// canonical path to revive a closed item is to file a fresh one. Other
// invalid combinations (none in the v1 matrix, but the default arm
// guards against unknown statuses) also surface this error.
var ErrInvalidTransition = errors.New("invalid state transition")

// Resolve transitions open → resolved. Clears verification_output (the
// resolve marks a fresh attempt, so any prior verifier failure output is
// no longer relevant). If note is non-empty it populates resolution_note;
// once set, resolution_note is preserved across subsequent transitions.
// From resolved this is a no-op (idempotent). From closed it is an
// error.
func Resolve(it *Item, note string, now time.Time) error {
	switch it.Status {
	case StatusOpen:
		it.Status = StatusResolved
		it.VerificationOutput = nil
		if note != "" {
			n := note
			it.ResolutionNote = &n
		}
		t := now
		it.ResolvedAt = &t
		return nil
	case StatusResolved:
		return nil // idempotent
	case StatusClosed:
		return ErrInvalidTransition
	}
	return ErrInvalidTransition
}

// Reopen transitions resolved → open, capturing the verifier output (or
// other caller-supplied reason) as verification_output so prior evidence
// survives as an audit trail. The previously-set resolved_at is cleared
// because the item is no longer in the resolved state. From open this is
// a no-op. From closed it is an error — re-filing is the canonical path
// to revive a closed item.
func Reopen(it *Item, output string, now time.Time) error {
	switch it.Status {
	case StatusResolved:
		it.Status = StatusOpen
		if output != "" {
			o := output
			it.VerificationOutput = &o
		} else {
			it.VerificationOutput = nil
		}
		it.ResolvedAt = nil
		return nil
	case StatusOpen:
		return nil // idempotent
	case StatusClosed:
		return ErrInvalidTransition
	}
	return ErrInvalidTransition
}

// Close transitions to closed.
//   - From open: cancel/dismiss path. If reason is non-empty it
//     populates resolution_note.
//   - From resolved: normal close. If reason is non-empty it populates
//     resolution_note (a no-verifier close where the caller wants to
//     record context); any resolve-time note is otherwise preserved.
//     If a verifier is attached, verified_at is set to now — the close
//     from resolved encodes "a verifier exited 0".
//   - From closed: no-op (idempotent).
//
// closed_at is set on every successful transition to closed.
func Close(it *Item, reason string, now time.Time) error {
	switch it.Status {
	case StatusOpen, StatusResolved:
		if reason != "" {
			r := reason
			it.ResolutionNote = &r
		}
		if it.Status == StatusResolved && it.Verifier != nil {
			t := now
			it.VerifiedAt = &t
		}
		it.Status = StatusClosed
		t := now
		it.ClosedAt = &t
		return nil
	case StatusClosed:
		return nil // idempotent
	}
	return ErrInvalidTransition
}
