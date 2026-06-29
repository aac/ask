// Package mcp — tool registry and per-tool handlers. Each tool mirrors one
// CLI verb (spec §5); handlers call into internal/core directly rather
// than shelling through cli.Run so the MCP path and CLI path share the
// store/transition layer but not the flag-parsing layer.
//
// Error mapping (spec §5.5):
//   - Bad input / unknown enum / state-machine refusals → code 2.
//   - Id prefix matched zero items                       → code 3.
//   - Id prefix matched >1 items                         → code 4.
//   - I/O failure (open store, read/write items)         → code 5.
//   - Idempotent no-op (resolve on resolved, close on closed, reopen on
//     open) → isError=false with two content parts: the unchanged Item
//     payload, followed by a `ask <verb>: already <status>` warning
//     string. This is the MCP analog of the CLI's exit-6 envelope
//     (stderr warning + stdout success body); see spec §5.5.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aac/ask/internal/core"
)

// maxBlocksRefLen caps a single `blocks` ref. The CLI uses the same
// constant (internal/cli.maxBlocksRefLen); duplicated here so the MCP
// package does not depend on the CLI package. 1024 matches the bound
// sketched in act-2a1b.
const maxBlocksRefLen = 1024

// maxRecipientRefLen caps a `recipient` ref. Same bound as a single
// blocks ref. Mirrors the CLI constant (internal/cli.maxRecipientRefLen);
// duplicated to keep MCP independent of the CLI package.
const maxRecipientRefLen = 1024

// tools returns the static tool descriptors advertised by tools/list. Order
// matches the CLI verb listing in internal/cli/root.go to make manual diffing
// trivial.
func tools() []toolDescriptor {
	return []toolDescriptor{
		{
			Name:        "ask_new",
			Description: "File a new ask. Returns the created Item object (spec §1.1).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"title"},
				"additionalProperties": false,
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"body":        map[string]any{"type": "string", "maxLength": 16384},
					"urgency":     map[string]any{"type": "string", "enum": []string{"blocker", "normal", "fyi"}},
					"filed_by":    map[string]any{"type": "string", "maxLength": 256},
					"recipient":   map[string]any{"type": "string", "minLength": 1, "maxLength": maxRecipientRefLen},
					"tracker_ref": map[string]any{"type": "string", "maxLength": 256},
					"verifier": map[string]any{
						"type":                 "object",
						"required":             []string{"command"},
						"additionalProperties": false,
						"properties": map[string]any{
							"type":            map[string]any{"type": "string", "enum": []string{"shell"}},
							"command":         map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
							"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600},
						},
					},
					"links": map[string]any{
						"type":     "array",
						"maxItems": 32,
						"items": map[string]any{
							"type":                 "object",
							"required":             []string{"label", "url"},
							"additionalProperties": false,
							"properties": map[string]any{
								"label": map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
								"url":   map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
							},
						},
					},
					"blocks": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":      "string",
							"minLength": 1,
							"maxLength": maxBlocksRefLen,
						},
					},
				},
			},
		},
		{
			Name:        "ask_list",
			Description: "List asks. Defaults to status != closed; pass `all: true` for every state (spec §3, §5.3).",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"status": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string", "enum": []string{"open", "resolved", "closed"}},
					},
					"urgency": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string", "enum": []string{"blocker", "normal", "fyi"}},
					},
					"blocks": map[string]any{
						"type":      "string",
						"minLength": 1,
						"maxLength": maxBlocksRefLen,
					},
					"recipient": map[string]any{
						"type":      "string",
						"minLength": 1,
						"maxLength": maxRecipientRefLen,
					},
					"all": map[string]any{"type": "boolean"},
				},
			},
		},
		{
			Name:        "ask_show",
			Description: "Show one ask by id or hex prefix. Returns the Item object (spec §1.5).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"id"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "minLength": 1},
				},
			},
		},
		{
			Name:        "ask_resolve",
			Description: "Transition an ask from open to resolved. Optional `note` populates resolution_note (spec §1.8).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"id"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":   map[string]any{"type": "string", "minLength": 1},
					"note": map[string]any{"type": "string", "maxLength": 16384},
				},
			},
		},
		{
			Name:        "ask_reopen",
			Description: "Transition a resolved ask back to open, capturing optional `reason` as verification_output (spec §1.8).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"id"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "minLength": 1},
					"reason": map[string]any{"type": "string", "maxLength": 16384},
				},
			},
		},
		{
			Name:        "ask_close",
			Description: "Close an ask (open→closed cancel/dismiss, or resolved→closed normal close). Optional `reason` (spec §1.8).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"id"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "minLength": 1},
					"reason": map[string]any{"type": "string", "maxLength": 16384},
				},
			},
		},
	}
}

// ----- ask_new ---------------------------------------------------------------

// newArgs mirrors the ask_new inputSchema. urgency defaults to "normal"
// when absent (matching the CLI's flag default). The verifier sub-object
// matches the core.Verifier shape one-to-one; timeout_seconds is required
// in the spec table (§1.2) but the CLI's `--timeout` defaults to 0, so we
// don't enforce a default here.
type newArgs struct {
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Urgency    string        `json:"urgency"`
	FiledBy    string        `json:"filed_by"`
	Recipient  string        `json:"recipient"`
	TrackerRef string        `json:"tracker_ref"`
	Verifier   *verifierArgs `json:"verifier"`
	Links      []core.Link   `json:"links"`
	Blocks     []string      `json:"blocks"`
}

type verifierArgs struct {
	Type           string `json:"type"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func callNew(repoRoot string, raw json.RawMessage) (string, bool) {
	var args newArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return encodeErr(2, "ask new: "+err.Error()), true
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		return encodeErr(2, "ask new: title is required"), true
	}
	if len(title) > 200 {
		return encodeErr(2, "ask new: title must be 1..200 characters"), true
	}
	if strings.ContainsAny(title, "\n\r") {
		return encodeErr(2, "ask new: title must not contain newlines"), true
	}
	urgency := args.Urgency
	if urgency == "" {
		urgency = string(core.UrgencyNormal)
	}
	u := core.Urgency(urgency)
	if !u.Valid() {
		return encodeErr(2, fmt.Sprintf("ask new: invalid urgency %q (want blocker|normal|fyi)", urgency)), true
	}
	// Spec §1.2: v1 only accepts verifier.type "shell"; url/mcp are reserved.
	if args.Verifier != nil {
		vt := args.Verifier.Type
		if vt == "" {
			vt = string(core.VerifierShell)
		}
		if vt != string(core.VerifierShell) {
			return encodeErr(2, fmt.Sprintf("ask new: invalid verifier type %q (only %q in v1)", vt, core.VerifierShell)), true
		}
		if strings.TrimSpace(args.Verifier.Command) == "" {
			return encodeErr(2, "ask new: verifier.command is required"), true
		}
	}
	// Each blocks ref must be non-empty after trim and within the
	// per-ref cap. ask never interprets the format; this is the only
	// validation. Mirrors the CLI rules in internal/cli/new.go.
	for _, ref := range args.Blocks {
		if strings.TrimSpace(ref) == "" {
			return encodeErr(2, "ask new: blocks: ref must not be empty or whitespace-only"), true
		}
		if len(ref) > maxBlocksRefLen {
			return encodeErr(2, fmt.Sprintf("ask new: blocks: ref exceeds %d characters", maxBlocksRefLen)), true
		}
	}
	// recipient (when present) is validated the same way as a single
	// blocks ref: non-empty after trim, within the per-ref cap. Absent
	// (== "") means no recipient; that's fine.
	if args.Recipient != "" {
		if strings.TrimSpace(args.Recipient) == "" {
			return encodeErr(2, "ask new: recipient: ref must not be empty or whitespace-only"), true
		}
		if len(args.Recipient) > maxRecipientRefLen {
			return encodeErr(2, fmt.Sprintf("ask new: recipient: ref exceeds %d characters", maxRecipientRefLen)), true
		}
	}

	store, err := core.OpenStore(repoRoot, nil)
	if err != nil {
		return encodeErr(5, "ask new: "+err.Error()), true
	}
	now := time.Now().UTC()
	existing, err := store.ListIDs()
	if err != nil {
		return encodeErr(5, "ask new: "+err.Error()), true
	}
	set := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		set[id] = struct{}{}
	}
	exists := func(id string) bool { _, ok := set[id]; return ok }

	id, err := core.NewID(store.Config().ProjectID, now, title, exists)
	if err != nil {
		return encodeErr(5, "ask new: "+err.Error()), true
	}
	item := core.NewItem(id, title, u, core.StatusOpen, now)
	item.Body = args.Body
	if args.FiledBy != "" {
		v := args.FiledBy
		item.FiledBy = &v
	}
	if args.TrackerRef != "" {
		v := args.TrackerRef
		item.TrackerRef = &v
	}
	if args.Recipient != "" {
		v := args.Recipient
		item.Recipient = &v
	}
	if args.Links != nil {
		item.Links = args.Links
	}
	if len(args.Blocks) > 0 {
		item.Blocks = append([]string{}, args.Blocks...)
	}
	if args.Verifier != nil {
		item.Verifier = &core.Verifier{
			Type:           core.VerifierShell,
			Command:        args.Verifier.Command,
			TimeoutSeconds: args.Verifier.TimeoutSeconds,
		}
	}
	if err := store.Save(item); err != nil {
		return encodeErr(5, "ask new: "+err.Error()), true
	}
	return encodeJSON(item)
}

// ----- ask_list --------------------------------------------------------------

type listArgs struct {
	Status    []string `json:"status"`
	Urgency   []string `json:"urgency"`
	Blocks    string   `json:"blocks"`
	Recipient string   `json:"recipient"`
	All       bool     `json:"all"`
}

func callList(repoRoot string, raw json.RawMessage) (string, bool) {
	var args listArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return encodeErr(2, "ask list: "+err.Error()), true
	}
	// Spec §5.3: `all: true` + a status value is a validation error.
	if args.All && len(args.Status) > 0 {
		return encodeErr(2, "ask list: --all and --status are mutually exclusive"), true
	}
	for _, s := range args.Status {
		if !core.Status(s).Valid() {
			return encodeErr(2, fmt.Sprintf("ask list: invalid status %q (want one of open, resolved, closed)", s)), true
		}
	}
	for _, u := range args.Urgency {
		if !core.Urgency(u).Valid() {
			return encodeErr(2, fmt.Sprintf("ask list: invalid urgency %q (want one of blocker, normal, fyi)", u)), true
		}
	}
	// MCP `blocks` filter is a single string (mirrors the CLI's
	// per-flag shape but flattened: only one ref is sensible per call
	// from an orchestrator that knows the act id it's coordinating).
	if args.Blocks != "" {
		if strings.TrimSpace(args.Blocks) == "" {
			return encodeErr(2, "ask list: blocks: ref must not be empty or whitespace-only"), true
		}
		if len(args.Blocks) > maxBlocksRefLen {
			return encodeErr(2, fmt.Sprintf("ask list: blocks: ref exceeds %d characters", maxBlocksRefLen)), true
		}
	}
	// MCP `recipient` filter is a single string (same convention as
	// `blocks`). Validated the same way: non-empty after trim and
	// within the per-ref cap.
	if args.Recipient != "" {
		if strings.TrimSpace(args.Recipient) == "" {
			return encodeErr(2, "ask list: recipient: ref must not be empty or whitespace-only"), true
		}
		if len(args.Recipient) > maxRecipientRefLen {
			return encodeErr(2, fmt.Sprintf("ask list: recipient: ref exceeds %d characters", maxRecipientRefLen)), true
		}
	}
	store, err := core.OpenStore(repoRoot, nil)
	if err != nil {
		return encodeErr(5, "ask list: "+err.Error()), true
	}
	items, err := store.List()
	if err != nil {
		return encodeErr(5, "ask list: "+err.Error()), true
	}
	filtered := make([]*core.Item, 0, len(items))
	for _, it := range items {
		if !statusMatches(it.Status, args.Status, args.All) {
			continue
		}
		if !urgencyMatches(it.Urgency, args.Urgency) {
			continue
		}
		if !blocksMatches(it.Blocks, args.Blocks) {
			continue
		}
		if !recipientMatches(it.Recipient, args.Recipient) {
			continue
		}
		filtered = append(filtered, it)
	}
	// Spec §1.4: empty array (never null) when nothing matches.
	if filtered == nil {
		filtered = []*core.Item{}
	}
	return encodeJSON(filtered)
}

// statusMatches and urgencyMatches mirror the CLI helpers in
// internal/cli/list.go (kept private here so the MCP layer doesn't take a
// hard dep on the cli package). The semantics are identical: explicit
// status list wins over `all`, which wins over the default
// "exclude-closed" filter.
func statusMatches(s core.Status, want []string, all bool) bool {
	if len(want) > 0 {
		for _, w := range want {
			if string(s) == w {
				return true
			}
		}
		return false
	}
	if all {
		return true
	}
	return s != core.StatusClosed
}

func urgencyMatches(u core.Urgency, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if string(u) == w {
			return true
		}
	}
	return false
}

// blocksMatches applies the MCP ask_list `blocks` filter. Empty filter
// includes every item. Otherwise the item's Blocks array must contain
// the requested ref (exact match). Matches the CLI helper of the same
// name but takes a single-string filter to mirror the MCP arg shape.
func blocksMatches(have []string, want string) bool {
	if want == "" {
		return true
	}
	for _, h := range have {
		if h == want {
			return true
		}
	}
	return false
}

// recipientMatches applies the MCP ask_list `recipient` filter. Empty
// filter ("") includes every item. Otherwise the item's Recipient
// field (a single optional string) must equal the requested ref
// (exact match). Items without a recipient set never match an
// explicit filter — there is no sentinel for "implicit human"; callers
// that want those omit the filter and post-filter the result.
func recipientMatches(have *string, want string) bool {
	if want == "" {
		return true
	}
	if have == nil {
		return false
	}
	return *have == want
}

// ----- ask_show / ask_resolve / ask_reopen / ask_close -----------------------

type idArgs struct {
	ID     string `json:"id"`
	Note   string `json:"note"`
	Reason string `json:"reason"`
}

func callShow(repoRoot string, raw json.RawMessage) (string, bool) {
	var args idArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return encodeErr(2, "ask show: "+err.Error()), true
	}
	if strings.TrimSpace(args.ID) == "" {
		return encodeErr(2, "ask show: id required"), true
	}
	store, full, code, msg, ok := resolveID(repoRoot, "show", args.ID)
	if !ok {
		return encodeErr(code, msg), true
	}
	it, err := store.Load(full)
	if err != nil {
		if errors.Is(err, core.ErrIDNotFound) {
			return encodeErr(3, fmt.Sprintf("ask show: id %q not found", full)), true
		}
		return encodeErr(5, "ask show: "+err.Error()), true
	}
	return encodeJSON(it)
}

func callResolve(repoRoot string, raw json.RawMessage) ([]toolContent, bool) {
	return mutate(repoRoot, "resolve", raw, func(args idArgs, it *core.Item) error {
		return core.Resolve(it, args.Note, time.Now().UTC())
	})
}

func callReopen(repoRoot string, raw json.RawMessage) ([]toolContent, bool) {
	return mutate(repoRoot, "reopen", raw, func(args idArgs, it *core.Item) error {
		return core.Reopen(it, args.Reason, time.Now().UTC())
	})
}

func callClose(repoRoot string, raw json.RawMessage) ([]toolContent, bool) {
	return mutate(repoRoot, "close", raw, func(args idArgs, it *core.Item) error {
		return core.Close(it, args.Reason, time.Now().UTC())
	})
}

// mutate is the shared lifecycle helper for ask_resolve / ask_reopen /
// ask_close. It mirrors the CLI's internal/cli.mutate: on a real state
// change it persists the item and returns a single text part with the
// post-transition Item JSON; on an idempotent no-op (the target status
// already matched, so core.Resolve / Reopen / Close returned nil
// without mutating the item) it skips the re-save and returns two text
// parts — the unchanged Item JSON followed by a warning string
// `ask <verb>: already <status>`. isError stays false for the no-op
// path because the call succeeded; see spec §5.5.
//
// On any error (validation, not-found, ambiguous, I/O), returns a
// single content part with the encoded errEnvelope and isError=true.
func mutate(repoRoot, verb string, raw json.RawMessage, fn func(idArgs, *core.Item) error) ([]toolContent, bool) {
	var args idArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return textContent(encodeErr(2, fmt.Sprintf("ask %s: %s", verb, err.Error()))), true
	}
	if strings.TrimSpace(args.ID) == "" {
		return textContent(encodeErr(2, fmt.Sprintf("ask %s: id required", verb))), true
	}
	store, full, code, msg, ok := resolveID(repoRoot, verb, args.ID)
	if !ok {
		return textContent(encodeErr(code, msg)), true
	}
	it, err := store.Load(full)
	if err != nil {
		if errors.Is(err, core.ErrIDNotFound) {
			return textContent(encodeErr(3, fmt.Sprintf("ask %s: id %q not found", verb, full))), true
		}
		return textContent(encodeErr(5, fmt.Sprintf("ask %s: %s", verb, err.Error()))), true
	}
	prev := it.Status
	if err := fn(args, it); err != nil {
		return textContent(encodeErr(2, fmt.Sprintf("ask %s: %s", verb, err.Error()))), true
	}
	// Detect idempotent no-op the same way internal/cli.mutate does
	// (post-call status comparison): core.Resolve/Reopen/Close return
	// nil and leave it.Status unchanged when the item is already in
	// the target state. We never re-Save on no-op — nothing changed,
	// and skipping the write avoids touching the file mtime for a
	// non-mutation.
	noop := it.Status == prev
	if !noop {
		if err := store.Save(it); err != nil {
			return textContent(encodeErr(5, fmt.Sprintf("ask %s: %s", verb, err.Error()))), true
		}
	}
	body, isErr := encodeJSON(it)
	if isErr {
		return textContent(body), true
	}
	if noop {
		// Spec §5.5: idempotent no-op surfaces as isError=false with
		// the item payload plus a second text content part carrying
		// the same warning string the CLI writes to stderr.
		return []toolContent{
			{Type: "text", Text: body},
			{Type: "text", Text: fmt.Sprintf("ask %s: already %s", verb, it.Status)},
		}, false
	}
	return textContent(body), false
}

// resolveID opens the store and resolves an id-or-prefix to a full id.
// On success returns (store, fullID, 0, "", true). On any failure returns
// the appropriate (exit-code, stderr-style message, false) tuple so the
// caller can wrap it in an errEnvelope. The verb argument is used only
// for the message prefix.
func resolveID(repoRoot, verb, raw string) (*core.FileStore, string, int, string, bool) {
	store, err := core.OpenStore(repoRoot, nil)
	if err != nil {
		return nil, "", 5, fmt.Sprintf("ask %s: %s", verb, err.Error()), false
	}
	ids, err := store.ListIDs()
	if err != nil {
		return nil, "", 5, fmt.Sprintf("ask %s: %s", verb, err.Error()), false
	}
	full, err := core.ResolvePrefix(raw, ids)
	if err != nil {
		switch {
		case errors.Is(err, core.ErrIDNotFound):
			return nil, "", 3, fmt.Sprintf("ask %s: id %q not found", verb, raw), false
		case errors.Is(err, core.ErrIDAmbiguous):
			return nil, "", 4, fmt.Sprintf("ask %s: %s", verb, err.Error()), false
		default:
			return nil, "", 5, fmt.Sprintf("ask %s: %s", verb, err.Error()), false
		}
	}
	return store, full, 0, "", true
}
