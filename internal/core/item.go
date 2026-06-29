package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// Urgency is the agent-stated importance of an item.
type Urgency string

const (
	UrgencyBlocker Urgency = "blocker"
	UrgencyNormal  Urgency = "normal"
	UrgencyFYI     Urgency = "fyi"
)

// Valid reports whether u is a known Urgency value.
func (u Urgency) Valid() bool {
	switch u {
	case UrgencyBlocker, UrgencyNormal, UrgencyFYI:
		return true
	}
	return false
}

// Status is the lifecycle state of an item.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
	StatusClosed   Status = "closed"
)

// Valid reports whether s is a known Status value.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusResolved, StatusClosed:
		return true
	}
	return false
}

// VerifierType is the kind of verifier attached to an item.
type VerifierType string

// VerifierShell is the only verifier type in v1. ask stores the command but
// does not execute it; the caller or agent that picks up the resolved item is
// responsible for running it via the host shell (see docs/spec.md §9).
const VerifierShell VerifierType = "shell"

// Verifier describes how an agent can confirm a human-side request was
// satisfied. v1 supports only a shell command verifier.
type Verifier struct {
	Type           VerifierType `json:"type"`
	Command        string       `json:"command"`
	TimeoutSeconds int          `json:"timeout_seconds"`
}

// Link is a labeled URL attached to an item (e.g. a console link).
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// CurrentSchemaVersion is the schema_version value written to every new
// Item on disk. Bumping this value is a deliberate, breaking-or-additive
// migration event — see docs/spec.md §12 and docs/distribution-readiness.md
// §6 for the policy. Read paths that encounter an empty schema_version
// treat it as CurrentSchemaVersion for backward compatibility with stores
// created before the field was introduced.
const CurrentSchemaVersion = "1"

// Item is the canonical agent-to-human request record. The on-disk
// representation under .ask/items/<id>.json is the JSON marshalling of
// this struct.
//
// Per spec §1.1: `omitempty` is NOT used; every field is always present
// in serialized output with `null` for unset optionals. Nullable string
// fields use `*string` so JSON null is distinguishable from "" (which is
// a valid set-to-empty value). `Body` is required and defaults to "" per
// spec, so it stays a plain `string`. The `Links` slice must serialize as
// `[]` (not `null`) when empty; callers initialize it via NewItem or
// equivalent. The `Blocks` slice follows the same always-emit-array
// convention as `Links` — opaque cross-tool refs (typically `act-XXXX`,
// but ask never interprets the format). The `Recipient` field is an
// optional free-form string naming who should pick up the ask (absent =
// implicit human; example values include `agent:data-prep`,
// `human:andrew`, `team:reviewers`); ask never interprets the format.
//
// SchemaVersion is the on-disk schema marker for `.ask/items/*.json`
// (spec §1.1, §12). New items written via NewItem (and any item passed
// through FileStore.Save) carry CurrentSchemaVersion. Read paths default
// an empty value to CurrentSchemaVersion so items created before the
// field existed continue to load. Agents reading `.ask/items/` directly
// MUST check this field; missing/empty means "version 1" for the
// lifetime of the v1 schema.
type Item struct {
	SchemaVersion      string     `json:"schema_version"`
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Body               string     `json:"body"`
	Urgency            Urgency    `json:"urgency"`
	Status             Status     `json:"status"`
	FiledBy            *string    `json:"filed_by"`
	Recipient          *string    `json:"recipient"`
	TrackerRef         *string    `json:"tracker_ref"`
	Verifier           *Verifier  `json:"verifier"`
	Links              []Link     `json:"links"`
	Blocks             []string   `json:"blocks"`
	ResolutionNote     *string    `json:"resolution_note"`
	VerificationOutput *string    `json:"verification_output"`
	CreatedAt          time.Time  `json:"created_at"`
	ResolvedAt         *time.Time `json:"resolved_at"`
	VerifiedAt         *time.Time `json:"verified_at"`
	ClosedAt           *time.Time `json:"closed_at"`
}

// NewItem returns an Item with the required fields populated and the
// `Links` and `Blocks` slices initialized to empty (non-nil) so JSON
// serialization emits `[]` rather than `null` (spec §1.1). Optional
// nullable string fields remain nil (serialize as `null`).
func NewItem(id, title string, urgency Urgency, status Status, createdAt time.Time) *Item {
	return &Item{
		SchemaVersion: CurrentSchemaVersion,
		ID:            id,
		Title:         title,
		Urgency:       urgency,
		Status:        status,
		CreatedAt:     createdAt,
		Links:         []Link{},
		Blocks:        []string{},
	}
}

// UnmarshalJSON decodes b into i and validates the urgency and status
// enums. Unknown enum values are rejected so malformed item files surface
// loudly instead of silently round-tripping. Per spec §1.1 the Links and
// Blocks slices must never be nil — normalize a missing or null entry to
// `[]` so callers can range over them unconditionally.
func (i *Item) UnmarshalJSON(b []byte) error {
	type raw Item
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if !r.Urgency.Valid() {
		return fmt.Errorf("invalid urgency %q", r.Urgency)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("invalid status %q", r.Status)
	}
	if r.Links == nil {
		r.Links = []Link{}
	}
	if r.Blocks == nil {
		r.Blocks = []string{}
	}
	// Backward-compat: items written before schema_version existed lack
	// the field; treat absent/empty as CurrentSchemaVersion silently so
	// existing on-disk stores continue to load (spec §1.1, §12;
	// distribution-readiness §6).
	if r.SchemaVersion == "" {
		r.SchemaVersion = CurrentSchemaVersion
	}
	*i = Item(r)
	return nil
}
