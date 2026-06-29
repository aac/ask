// Package core implements ask's domain primitives: item identity, on-disk
// state, and the small set of pure helpers shared by the CLI and MCP layers.
//
// This file owns id generation and prefix lookup. It is deliberately free of
// dependencies on the Item or store types so that it can be reviewed and tested
// in isolation; callers pass in raw fields and an `exists` callback.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// idPrefix is the literal "ask-" prefix every id carries.
	idPrefix = "ask-"
	// idHexLen is the number of hex characters after the prefix.
	// Spec §4.1: first 4 hex chars (16 bits) of the sha256 digest.
	idHexLen = 4
	// maxIDRetries is the hard cap on collision retries. Spec §4.2.3.
	maxIDRetries = 1024
)

// ErrIDNotFound is returned when a prefix matches no id in the pool.
// Maps to CLI exit code 3 (spec §4.3).
var ErrIDNotFound = errors.New("ask id not found")

// ErrIDAmbiguous is returned when a prefix matches more than one id.
// Maps to CLI exit code 4 (spec §4.3). The wrapped error carries the
// matching ids so the caller can surface them on stderr.
var ErrIDAmbiguous = errors.New("ask id prefix is ambiguous")

// NewID generates a fresh ask id from (projectID, createdAt, title) per
// spec §4.1. It hashes those three fields with sha256 and takes the first
// idHexLen hex chars. If the candidate id collides with an existing one
// (as determined by the `exists` callback), createdAt is nudged forward
// by 1ns and the hash is recomputed; up to maxIDRetries attempts.
//
// The createdAt value is formatted as RFC 3339 with nanosecond precision;
// callers are responsible for ensuring the time is in UTC if they want
// the hash input to match the on-disk created_at exactly (spec §4.1).
//
// On overflow, returns a non-nil error; CLI callers translate this to
// exit code 5 with the spec-mandated stderr message (spec §4.2.3).
func NewID(projectID string, createdAt time.Time, title string, exists func(string) bool) (string, error) {
	t := createdAt
	for i := 0; i < maxIDRetries; i++ {
		sum := sha256.Sum256([]byte(projectID + t.Format(time.RFC3339Nano) + title))
		id := idPrefix + hex.EncodeToString(sum[:])[:idHexLen]
		if !exists(id) {
			return id, nil
		}
		t = t.Add(time.Nanosecond)
	}
	return "", fmt.Errorf("id collision retries exceeded after %d attempts", maxIDRetries)
}

// ResolvePrefix takes a user-supplied id or hex prefix and resolves it to
// a single full id from the given pool. Per spec §4.3:
//
//   - Input is case-insensitive; "ASK-3C", "ask-3c", and "3c" are equivalent.
//   - The "ask-" prefix is optional.
//   - An empty (after-prefix) input is ErrIDNotFound.
//   - 0 matches → ErrIDNotFound (CLI exit 3).
//   - 1 match → returned as-is.
//   - >1 matches → ErrIDAmbiguous wrapped with the list of matches (CLI exit 4).
//
// The pool is the set of full ids known to the store; this function does
// not touch disk.
func ResolvePrefix(input string, pool []string) (string, error) {
	needle := strings.TrimPrefix(strings.ToLower(input), idPrefix)
	if needle == "" {
		return "", ErrIDNotFound
	}
	var matches []string
	for _, id := range pool {
		tail := strings.TrimPrefix(strings.ToLower(id), idPrefix)
		if strings.HasPrefix(tail, needle) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", ErrIDNotFound
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%w: %s", ErrIDAmbiguous, strings.Join(matches, ", "))
	}
}
