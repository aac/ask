package core

import (
	"testing"
	"time"
)

func mustItem(id string, status Status) *Item {
	return &Item{ID: id, Title: "t", Urgency: UrgencyNormal, Status: status, CreatedAt: time.Now()}
}

func TestResolveFromOpen(t *testing.T) {
	it := mustItem("ask-1234", StatusOpen)
	stale := "stale verifier error"
	it.VerificationOutput = &stale
	if err := Resolve(it, "credentials at .env.local", time.Now()); err != nil {
		t.Fatal(err)
	}
	if it.Status != StatusResolved {
		t.Fatalf("status: %s", it.Status)
	}
	if it.VerificationOutput != nil {
		t.Fatal("verification_output should be cleared on resolve")
	}
	if it.ResolutionNote == nil || *it.ResolutionNote != "credentials at .env.local" {
		t.Fatal("note not set")
	}
	if it.ResolvedAt == nil {
		t.Fatal("resolved_at not set")
	}
}

func TestResolveFromResolvedIsNoop(t *testing.T) {
	it := mustItem("ask-1234", StatusResolved)
	if err := Resolve(it, "", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFromClosedIsError(t *testing.T) {
	it := mustItem("ask-1234", StatusClosed)
	if err := Resolve(it, "", time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestReopenFromResolved(t *testing.T) {
	it := mustItem("ask-1234", StatusResolved)
	now := time.Now()
	it.ResolvedAt = &now
	if err := Reopen(it, "verifier output here", time.Now()); err != nil {
		t.Fatal(err)
	}
	if it.Status != StatusOpen {
		t.Fatalf("status: %s", it.Status)
	}
	if it.VerificationOutput == nil || *it.VerificationOutput != "verifier output here" {
		t.Fatal("verification_output not set")
	}
}

func TestReopenFromOpenIsNoop(t *testing.T) {
	it := mustItem("ask-1234", StatusOpen)
	if err := Reopen(it, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestReopenFromClosedIsError(t *testing.T) {
	it := mustItem("ask-1234", StatusClosed)
	if err := Reopen(it, "x", time.Now()); err == nil {
		t.Fatal("expected error: re-filing is the canonical path")
	}
}

func TestCloseFromOpenSetsResolutionNote(t *testing.T) {
	it := mustItem("ask-1234", StatusOpen)
	if err := Close(it, "no longer needed", time.Now()); err != nil {
		t.Fatal(err)
	}
	if it.Status != StatusClosed {
		t.Fatalf("status: %s", it.Status)
	}
	if it.ResolutionNote == nil || *it.ResolutionNote != "no longer needed" {
		t.Fatal("note not set on cancel-dismiss")
	}
	if it.ClosedAt == nil {
		t.Fatal("closed_at not set")
	}
}

func TestCloseFromResolvedPreservesNoteAndSetsVerifiedAt(t *testing.T) {
	it := mustItem("ask-1234", StatusResolved)
	note := "from resolve"
	it.ResolutionNote = &note
	it.Verifier = &Verifier{Type: VerifierShell, Command: "true"}
	if err := Close(it, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if it.ResolutionNote == nil || *it.ResolutionNote != "from resolve" {
		t.Fatal("resolve-time note should be preserved")
	}
	if it.VerifiedAt == nil {
		t.Fatal("verified_at should be set when closing from resolved with a verifier")
	}
}

func TestCloseFromResolvedNoVerifierDoesNotSetVerifiedAt(t *testing.T) {
	it := mustItem("ask-1234", StatusResolved)
	if err := Close(it, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if it.VerifiedAt != nil {
		t.Fatal("verified_at should NOT be set if no verifier ran")
	}
}

func TestCloseFromClosedIsNoop(t *testing.T) {
	it := mustItem("ask-1234", StatusClosed)
	if err := Close(it, "", time.Now()); err != nil {
		t.Fatal(err)
	}
}
