package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// vtoItemJSON is an item file as `ask new --verifier ...` wrote it before
// act-278a9d: a verifier whose timeout_seconds is whatever the caller
// passed, 0 when they passed nothing.
func vtoItemJSON(timeout string) string {
	return `{"schema_version":"1","id":"ask-0a1b","title":"old","body":"","urgency":"normal","status":"open",` +
		`"filed_by":null,"recipient":null,"tracker_ref":null,` +
		`"verifier":{"type":"shell","command":"true","timeout_seconds":` + timeout + `},` +
		`"links":[],"blocks":[],"resolution_note":null,"verification_output":null,` +
		`"created_at":"2026-10-11T00:00:00Z","resolved_at":null,"verified_at":null,"closed_at":null}`
}

// TestStoredZeroTimeoutReadsAsDefault: an item on disk with
// verifier.timeout_seconds 0 still loads, and reads as the spec §1.2
// default. ask runs no verifier itself (§9); the read path is the one
// place every agent that does run one goes through, so that is where a
// stored 0 becomes 60. The read does not rewrite the file.
func TestStoredZeroTimeoutReadsAsDefault(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root, &ProjectConfig{ProjectID: "01HXYZTESTPROJECT00000000", DisplayName: "t", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	path := filepath.Join(root, ".ask", "items", "ask-0a1b.json")
	onDisk := vtoItemJSON("0")
	if err := os.WriteFile(path, []byte(onDisk), 0o644); err != nil {
		t.Fatalf("write item: %v", err)
	}

	it, err := store.Load("ask-0a1b")
	if err != nil {
		t.Fatalf("an item with a stored timeout of 0 must still load: %v", err)
	}
	if it.Verifier == nil || it.Verifier.TimeoutSeconds != DefaultVerifierTimeoutSeconds {
		t.Fatalf("stored 0 read as %+v, want timeout_seconds %d", it.Verifier, DefaultVerifierTimeoutSeconds)
	}
	items, skipped, err := store.List()
	if err != nil || len(skipped) != 0 || len(items) != 1 {
		t.Fatalf("list: items=%d skipped=%v err=%v", len(items), skipped, err)
	}
	if got := items[0].Verifier.TimeoutSeconds; got != DefaultVerifierTimeoutSeconds {
		t.Fatalf("list read stored 0 as %d, want %d", got, DefaultVerifierTimeoutSeconds)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if string(after) != onDisk {
		t.Fatalf("a read rewrote the item file:\n was %s\n now %s", onDisk, after)
	}
}

// TestStoredTimeoutOtherValuesAreLeftAlone: only 0 means "none given".
// An explicit value is returned as stored, including one outside 1..3600
// that an older `ask new --timeout` accepted — existing items are never
// refused or rewritten on read.
func TestStoredTimeoutOtherValuesAreLeftAlone(t *testing.T) {
	for raw, want := range map[string]int{"1": 1, "30": 30, "3600": 3600, "99999": 99999, "-5": -5} {
		var it Item
		if err := json.Unmarshal([]byte(vtoItemJSON(raw)), &it); err != nil {
			t.Fatalf("timeout %s: item refused: %v", raw, err)
		}
		if it.Verifier.TimeoutSeconds != want {
			t.Fatalf("timeout %s read as %d", raw, it.Verifier.TimeoutSeconds)
		}
	}
}

// TestNoVerifierStaysNull: the default applies to a verifier, never
// conjures one.
func TestNoVerifierStaysNull(t *testing.T) {
	var it Item
	raw := `{"id":"ask-0a1b","title":"t","urgency":"normal","status":"open","verifier":null,"created_at":"2026-10-11T00:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if it.Verifier != nil {
		t.Fatalf("verifier = %+v, want nil", it.Verifier)
	}
}
