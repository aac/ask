package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewIDIsHexFourChars(t *testing.T) {
	id, err := NewID("01HXYZ", time.Now(), "Set up OAuth", func(_ string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "ask-") || len(id) != len("ask-")+4 {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestNewIDRetriesOnCollision(t *testing.T) {
	tries := 0
	exists := func(_ string) bool {
		tries++
		return tries < 3
	}
	if _, err := NewID("p", time.Now(), "t", exists); err != nil {
		t.Fatal(err)
	}
	if tries < 3 {
		t.Fatalf("expected retries on collision, got %d", tries)
	}
}

func TestNewIDOverflowsAfterMaxRetries(t *testing.T) {
	if _, err := NewID("p", time.Now(), "t", func(_ string) bool { return true }); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestResolvePrefix(t *testing.T) {
	all := []string{"ask-3c89", "ask-3c8a", "ask-7ecd"}
	got, err := ResolvePrefix("3c89", all)
	if err != nil || got != "ask-3c89" {
		t.Fatalf("expected ask-3c89, got %q err=%v", got, err)
	}
	if _, err := ResolvePrefix("3c", all); err == nil {
		t.Fatal("expected ambiguous error")
	}
	if _, err := ResolvePrefix("9999", all); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestResolvePrefixAcceptsFullPrefixedInput(t *testing.T) {
	all := []string{"ask-3c89", "ask-7ecd"}
	got, err := ResolvePrefix("ask-3c89", all)
	if err != nil || got != "ask-3c89" {
		t.Fatalf("expected ask-3c89, got %q err=%v", got, err)
	}
}

func TestResolvePrefixIsCaseInsensitive(t *testing.T) {
	// Per spec §4.3: case-insensitive; "ASK-3C" must resolve like "3c".
	all := []string{"ask-3c89", "ask-7ecd"}
	got, err := ResolvePrefix("ASK-3C89", all)
	if err != nil || got != "ask-3c89" {
		t.Fatalf("expected ask-3c89, got %q err=%v", got, err)
	}
	got, err = ResolvePrefix("3C89", all)
	if err != nil || got != "ask-3c89" {
		t.Fatalf("expected ask-3c89, got %q err=%v", got, err)
	}
}

func TestResolvePrefixEmptyInputIsNotFound(t *testing.T) {
	all := []string{"ask-3c89"}
	if _, err := ResolvePrefix("", all); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("expected ErrIDNotFound, got %v", err)
	}
	if _, err := ResolvePrefix("ask-", all); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("expected ErrIDNotFound for bare prefix, got %v", err)
	}
}

func TestResolvePrefixErrorsAreSentinel(t *testing.T) {
	all := []string{"ask-3c89", "ask-3c8a"}
	_, err := ResolvePrefix("3c", all)
	if !errors.Is(err, ErrIDAmbiguous) {
		t.Fatalf("expected ErrIDAmbiguous, got %v", err)
	}
	_, err = ResolvePrefix("9999", all)
	if !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("expected ErrIDNotFound, got %v", err)
	}
}
