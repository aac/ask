package core

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestItemJSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	in := &Item{
		ID:         "ask-3c89",
		Title:      "Set up Gmail OAuth",
		Body:       "Create OAuth client at ...",
		Urgency:    UrgencyBlocker,
		Status:     StatusOpen,
		FiledBy:    strPtr("claude-code:session-abc"),
		TrackerRef: strPtr("act-3c89"),
		Verifier: &Verifier{
			Type:           VerifierShell,
			Command:        "pnpm test:gmail-auth",
			TimeoutSeconds: 60,
		},
		Links:     []Link{{Label: "console", URL: "https://example.com"}},
		CreatedAt: created,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Title != in.Title || out.Urgency != in.Urgency || out.Verifier.Command != in.Verifier.Command {
		t.Fatalf("round trip diverged: in=%+v out=%+v", in, out)
	}
	if out.FiledBy == nil || *out.FiledBy != *in.FiledBy {
		t.Fatalf("filed_by round trip diverged: %+v", out.FiledBy)
	}
}

func TestItemRejectsInvalidUrgency(t *testing.T) {
	raw := []byte(`{"id":"ask-1234","title":"x","urgency":"superduper","status":"open","created_at":"2026-05-15T10:30:00Z"}`)
	var out Item
	if err := json.Unmarshal(raw, &out); err == nil {
		t.Fatal("expected error on invalid urgency")
	}
}

// TestItemJSONEmitsAllFields enforces spec §1.1: every Item field is
// present in serialized output, with `null` for unset optionals (no
// `omitempty`), `""` for unset Body, and `[]` (not `null`) for empty
// Links / Blocks. Downstream parsers must never have to branch on key
// presence.
func TestItemJSONEmitsAllFields(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	item := NewItem("ask-3c89", "Set up Gmail OAuth", UrgencyNormal, StatusOpen, created)

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// All canonical keys per spec §1.1 must be present.
	wantKeys := []string{
		`"schema_version":`,
		`"id":`,
		`"title":`,
		`"body":`,
		`"urgency":`,
		`"status":`,
		`"filed_by":`,
		`"recipient":`,
		`"tracker_ref":`,
		`"verifier":`,
		`"links":`,
		`"blocks":`,
		`"resolution_note":`,
		`"verification_output":`,
		`"created_at":`,
		`"resolved_at":`,
		`"verified_at":`,
		`"closed_at":`,
	}
	s := string(b)
	for _, k := range wantKeys {
		if !strings.Contains(s, k) {
			t.Errorf("expected key %s in JSON output: %s", k, s)
		}
	}

	// Unset optional fields must serialize as null, not "" or absent.
	for _, nullField := range []string{
		`"filed_by":null`,
		`"recipient":null`,
		`"tracker_ref":null`,
		`"verifier":null`,
		`"resolution_note":null`,
		`"verification_output":null`,
		`"resolved_at":null`,
		`"verified_at":null`,
		`"closed_at":null`,
	} {
		if !strings.Contains(s, nullField) {
			t.Errorf("expected %s in JSON output: %s", nullField, s)
		}
	}

	// Body defaults to "" (spec §1.1: required, default ""); never null.
	if !strings.Contains(s, `"body":""`) {
		t.Errorf("expected body:\"\" in JSON output: %s", s)
	}

	// Links and Blocks must serialize as [] (not null) for empty.
	if !strings.Contains(s, `"links":[]`) {
		t.Errorf("expected links:[] in JSON output: %s", s)
	}
	if bytes.Contains(b, []byte(`"links":null`)) {
		t.Errorf("links should never serialize as null: %s", s)
	}
	if !strings.Contains(s, `"blocks":[]`) {
		t.Errorf("expected blocks:[] in JSON output: %s", s)
	}
	if bytes.Contains(b, []byte(`"blocks":null`)) {
		t.Errorf("blocks should never serialize as null: %s", s)
	}
}

// TestItemJSONNullableStringsDistinctFromEmpty verifies the *string
// indirection lets callers distinguish "set to empty" from "not set" —
// a deliberate change from the v0 design where FiledBy was a plain
// string and both unset and "" collapsed to "". Per spec §1.1 the
// nullable fields are documented as `string|null`.
func TestItemJSONNullableStringsDistinctFromEmpty(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	empty := ""
	item := NewItem("ask-3c89", "t", UrgencyNormal, StatusOpen, created)
	item.FiledBy = &empty

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"filed_by":""`) {
		t.Fatalf("expected filed_by:\"\" (set-to-empty), got: %s", s)
	}
	if strings.Contains(s, `"filed_by":null`) {
		t.Fatalf("filed_by should not be null when set to \"\": %s", s)
	}
}

// TestItemUnmarshalNullableNullStays verifies that JSON null for a
// nullable field unmarshals back to nil (not &"").
func TestItemUnmarshalNullableNullStays(t *testing.T) {
	raw := []byte(`{
		"id": "ask-3c89",
		"title": "x",
		"body": "",
		"urgency": "normal",
		"status": "open",
		"filed_by": null,
		"recipient": null,
		"tracker_ref": null,
		"verifier": null,
		"links": [],
		"blocks": [],
		"resolution_note": null,
		"verification_output": null,
		"created_at": "2026-05-15T10:30:00Z",
		"resolved_at": null,
		"verified_at": null,
		"closed_at": null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if it.FiledBy != nil || it.Recipient != nil || it.TrackerRef != nil || it.ResolutionNote != nil || it.VerificationOutput != nil {
		t.Fatalf("nullable strings should remain nil for JSON null, got %+v", it)
	}
	if it.Links == nil {
		t.Fatalf("links should be non-nil empty slice after unmarshal")
	}
	if len(it.Links) != 0 {
		t.Fatalf("links should be empty, got %d", len(it.Links))
	}
	if it.Blocks == nil {
		t.Fatalf("blocks should be non-nil empty slice after unmarshal")
	}
	if len(it.Blocks) != 0 {
		t.Fatalf("blocks should be empty, got %d", len(it.Blocks))
	}
}

// TestItemBlocksRoundTrip pins that the blocks slice round-trips cleanly
// for both single-ref and multi-ref items and preserves ordering.
func TestItemBlocksRoundTrip(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	in := NewItem("ask-3c89", "t", UrgencyNormal, StatusOpen, created)
	in.Blocks = []string{"act-3c89", "linear-eng-1234", "https://example.com/issue/7"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Blocks) != 3 {
		t.Fatalf("blocks length: got %d want 3", len(out.Blocks))
	}
	for i, want := range in.Blocks {
		if out.Blocks[i] != want {
			t.Fatalf("blocks[%d]: got %q want %q", i, out.Blocks[i], want)
		}
	}
}

// TestItemSchemaVersionWrittenByNewItem pins that NewItem stamps every
// freshly created Item with CurrentSchemaVersion and that the field
// survives marshal -> unmarshal round-trip with the documented value
// "1" (distribution-readiness §6, spec §1.1, §12).
func TestItemSchemaVersionWrittenByNewItem(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	item := NewItem("ask-3c89", "x", UrgencyNormal, StatusOpen, created)
	if item.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("NewItem schema_version: got %q want %q", item.SchemaVersion, CurrentSchemaVersion)
	}
	if CurrentSchemaVersion != "1" {
		t.Fatalf("CurrentSchemaVersion changed without coordinated migration: got %q want %q", CurrentSchemaVersion, "1")
	}
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"schema_version":"1"`) {
		t.Fatalf("expected schema_version:\"1\" in JSON: %s", string(b))
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SchemaVersion != "1" {
		t.Fatalf("round-trip schema_version: got %q want %q", out.SchemaVersion, "1")
	}
}

// TestItemUnmarshalMissingSchemaVersionDefaultsToOne verifies the
// backward-compat read path: an item written before the field existed
// (i.e. with no schema_version key at all in its JSON) loads silently
// with SchemaVersion = "1". This is the load-bearing tolerance — the
// dogfood store on Andrew's laptop has dozens of these items today.
func TestItemUnmarshalMissingSchemaVersionDefaultsToOne(t *testing.T) {
	raw := []byte(`{
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],"blocks":[],
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal pre-schema-version item: %v", err)
	}
	if it.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("missing schema_version should default to %q, got %q", CurrentSchemaVersion, it.SchemaVersion)
	}
}

// TestItemUnmarshalEmptySchemaVersionDefaultsToOne verifies an explicit
// empty string is treated the same as missing — protects against any
// caller that constructs raw JSON with schema_version:"".
func TestItemUnmarshalEmptySchemaVersionDefaultsToOne(t *testing.T) {
	raw := []byte(`{
		"schema_version":"",
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],"blocks":[],
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal empty-schema-version item: %v", err)
	}
	if it.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("empty schema_version should default to %q, got %q", CurrentSchemaVersion, it.SchemaVersion)
	}
}

// TestItemUnmarshalPreservesExplicitSchemaVersion verifies a non-empty
// value is preserved verbatim — when "2" eventually exists, the read
// path must not silently rewrite it to "1".
func TestItemUnmarshalPreservesExplicitSchemaVersion(t *testing.T) {
	raw := []byte(`{
		"schema_version":"2",
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],"blocks":[],
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if it.SchemaVersion != "2" {
		t.Fatalf("explicit schema_version should be preserved, got %q", it.SchemaVersion)
	}
}

// TestItemRecipientRoundTrip pins that an explicit recipient survives
// marshal/unmarshal and that the field defaults to nil (serialized as
// null) when NewItem creates an item without one. The recipient is the
// agent-to-agent label introduced in act-341838; ask never interprets
// the format.
func TestItemRecipientRoundTrip(t *testing.T) {
	created := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	in := NewItem("ask-3c89", "t", UrgencyNormal, StatusOpen, created)
	rcpt := "agent:data-prep"
	in.Recipient = &rcpt
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"recipient":"agent:data-prep"`) {
		t.Fatalf("expected recipient in JSON: %s", b)
	}
	var out Item
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Recipient == nil || *out.Recipient != "agent:data-prep" {
		t.Fatalf("recipient round trip: got %+v", out.Recipient)
	}
}

// TestItemUnmarshalMissingRecipientStaysNil verifies the additive
// backward-compat path: items written before `recipient` existed lack
// the key entirely; reading them must yield Recipient == nil (the
// documented "absent = implicit human" state), not a crash or a default
// non-nil pointer to "".
func TestItemUnmarshalMissingRecipientStaysNil(t *testing.T) {
	raw := []byte(`{
		"schema_version":"1",
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],"blocks":[],
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal pre-recipient item: %v", err)
	}
	if it.Recipient != nil {
		t.Fatalf("missing recipient should stay nil, got %+v", it.Recipient)
	}
}

// TestItemUnmarshalBlocksNullNormalized verifies a missing or null
// `blocks` key in input still yields an empty (non-nil) slice — matches
// the Links normalization so downstream code can range without nil checks.
func TestItemUnmarshalBlocksNullNormalized(t *testing.T) {
	// blocks absent.
	raw := []byte(`{
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatalf("unmarshal absent-blocks: %v", err)
	}
	if it.Blocks == nil || len(it.Blocks) != 0 {
		t.Fatalf("absent blocks should normalize to non-nil empty, got %+v", it.Blocks)
	}
	// blocks: null.
	raw2 := []byte(`{
		"id":"ask-3c89","title":"x","body":"","urgency":"normal","status":"open",
		"filed_by":null,"tracker_ref":null,"verifier":null,"links":[],"blocks":null,
		"resolution_note":null,"verification_output":null,
		"created_at":"2026-05-15T10:30:00Z",
		"resolved_at":null,"verified_at":null,"closed_at":null
	}`)
	var it2 Item
	if err := json.Unmarshal(raw2, &it2); err != nil {
		t.Fatalf("unmarshal null-blocks: %v", err)
	}
	if it2.Blocks == nil || len(it2.Blocks) != 0 {
		t.Fatalf("null blocks should normalize to non-nil empty, got %+v", it2.Blocks)
	}
}
