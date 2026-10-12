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
//   - Store stranded: items present, config.json gone     → code 7.
//   - Idempotent no-op (resolve on resolved, close on closed, reopen on
//     open) → isError=false with two content parts: the unchanged Item
//     payload, followed by a `ask <verb>: already <status>` warning
//     string. This is the MCP analog of the CLI's exit-6 envelope
//     (stderr warning + stdout success body); see spec §5.5. ask_update
//     uses the same envelope when the item already reads as requested;
//     its warning is `ask update: <id> already reads as requested`.
//   - ask_list over a store with unreadable item files → isError=false
//     with the array of readable items plus one warning part per skipped
//     id; code 5 only when no item at all was readable (spec §7.2).
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
			Description: "List asks. Defaults to status != closed; pass `all: true` for every state (spec §3, §5.3). An item file that cannot be read is skipped: the array of readable items is content[0], and each skipped id adds a text part `ask list: skipped <id>: <reason>`.",
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
			Name:        "ask_update",
			Description: "Correct an ask's title or body in place. Makes no state transition: an open ask stays open. Give `title`, and/or one of `body` (replace; an empty string clears it) or `body_append` (add a note after a blank line). Returns the updated Item object (spec §1.8.1).",
			InputSchema: map[string]any{
				"type":                 "object",
				"required":             []string{"id"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "minLength": 1},
					"title":       map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"body":        map[string]any{"type": "string", "maxLength": 16384},
					"body_append": map[string]any{"type": "string", "maxLength": 16384},
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
		return encodeErr(storeErrCode(err), "ask new: "+err.Error()), true
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

// callList is the MCP twin of `ask list`. Unreadable item files follow
// spec §7.2 the way the CLI does: they are skipped and the rest are
// returned with isError=false, the array in content[0] and then one extra
// text part per skipped id carrying the line the CLI writes to stderr
// (`ask list: skipped <id>: <reason>`) — the §5.5 success-plus-warning
// shape. When files were skipped and no item at all was readable, the
// call is a code-5 error whose message names every skipped id.
func callList(repoRoot string, raw json.RawMessage) ([]toolContent, bool) {
	body, skipped, isErr := listBody(repoRoot, raw)
	parts := textContent(body)
	if isErr {
		return parts, true
	}
	for _, sk := range skipped {
		parts = append(parts, toolContent{Type: "text", Text: "ask list: " + sk.String()})
	}
	return parts, false
}

// listBody returns the JSON body for ask_list (the item array, or an
// error envelope with isErr=true) and, on success, the item files that
// were skipped as unreadable.
func listBody(repoRoot string, raw json.RawMessage) (body string, skipped []core.SkippedItem, isErr bool) {
	fail := func(code int, msg string) (string, []core.SkippedItem, bool) {
		return encodeErr(code, msg), nil, true
	}
	var args listArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fail(2, "ask list: "+err.Error())
	}
	// Spec §5.3: `all: true` + a status value is a validation error.
	if args.All && len(args.Status) > 0 {
		return fail(2, "ask list: --all and --status are mutually exclusive")
	}
	for _, s := range args.Status {
		if !core.Status(s).Valid() {
			return fail(2, fmt.Sprintf("ask list: invalid status %q (want one of open, resolved, closed)", s))
		}
	}
	for _, u := range args.Urgency {
		if !core.Urgency(u).Valid() {
			return fail(2, fmt.Sprintf("ask list: invalid urgency %q (want one of blocker, normal, fyi)", u))
		}
	}
	// MCP `blocks` filter is a single string (mirrors the CLI's
	// per-flag shape but flattened: only one ref is sensible per call
	// from an orchestrator that knows the act id it's coordinating).
	if args.Blocks != "" {
		if strings.TrimSpace(args.Blocks) == "" {
			return fail(2, "ask list: blocks: ref must not be empty or whitespace-only")
		}
		if len(args.Blocks) > maxBlocksRefLen {
			return fail(2, fmt.Sprintf("ask list: blocks: ref exceeds %d characters", maxBlocksRefLen))
		}
	}
	// MCP `recipient` filter is a single string (same convention as
	// `blocks`). Validated the same way: non-empty after trim and
	// within the per-ref cap.
	if args.Recipient != "" {
		if strings.TrimSpace(args.Recipient) == "" {
			return fail(2, "ask list: recipient: ref must not be empty or whitespace-only")
		}
		if len(args.Recipient) > maxRecipientRefLen {
			return fail(2, fmt.Sprintf("ask list: recipient: ref exceeds %d characters", maxRecipientRefLen))
		}
	}
	store, err := core.OpenStore(repoRoot, nil)
	if err != nil {
		return fail(storeErrCode(err), "ask list: "+err.Error())
	}
	items, skipped, err := store.List()
	if err != nil {
		return fail(5, "ask list: "+err.Error())
	}
	if len(skipped) > 0 && len(items) == 0 {
		// Spec §7.2: every item file was unreadable. Judged before the
		// filters run, as in the CLI. There is no stderr here, so the
		// one message carries every skipped id.
		reasons := make([]string, len(skipped))
		for i, sk := range skipped {
			reasons[i] = sk.String()
		}
		return fail(5, fmt.Sprintf("ask list: no readable items (%d unreadable): %s",
			len(skipped), strings.Join(reasons, "; ")))
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
	body, isErr = encodeJSON(filtered)
	if isErr {
		return body, nil, true
	}
	return body, skipped, false
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

// ----- ask_update ------------------------------------------------------------

// updateArgs mirrors the ask_update inputSchema. The text fields are
// pointers so "absent" and "explicit empty string" stay distinct: an
// omitted body must not blank the body, while `"body": ""` clears it —
// the same distinction `ask update` draws with flag.Visit. The CLI's
// --body-file / --body-append-file have no twin: an MCP caller already
// holds the text.
type updateArgs struct {
	ID         string  `json:"id"`
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	BodyAppend *string `json:"body_append"`
}

// callUpdate is the MCP twin of `ask update` (spec §1.8.1): same
// validation order, same messages, and the edit itself is the shared
// core.TextUpdate. It makes no state transition. On the idempotent no-op
// (the item already reads as requested) it skips the write and returns
// the §5.5 two-part envelope — the unchanged Item plus the warning line
// the CLI writes to stderr with exit 6.
func callUpdate(repoRoot string, raw json.RawMessage) ([]toolContent, bool) {
	var args updateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return textContent(encodeErr(2, "ask update: "+err.Error())), true
	}
	if args.Body != nil && args.BodyAppend != nil {
		return textContent(encodeErr(2, "ask update: body, body_append are mutually exclusive")), true
	}
	upd := core.TextUpdate{Title: args.Title, Body: args.Body, BodyAppend: args.BodyAppend}
	if upd.Empty() {
		return textContent(encodeErr(2, "ask update: nothing to update (want title, body, or body_append)")), true
	}
	if args.Title != nil {
		if msg, ok := core.ValidateTitle(*args.Title); !ok {
			return textContent(encodeErr(2, "ask update: "+msg)), true
		}
	}
	if strings.TrimSpace(args.ID) == "" {
		return textContent(encodeErr(2, "ask update: id required")), true
	}
	store, full, code, msg, ok := resolveID(repoRoot, "update", args.ID)
	if !ok {
		return textContent(encodeErr(code, msg)), true
	}
	it, err := store.Load(full)
	if err != nil {
		if errors.Is(err, core.ErrIDNotFound) {
			return textContent(encodeErr(3, fmt.Sprintf("ask update: id %q not found", full))), true
		}
		return textContent(encodeErr(5, "ask update: "+err.Error())), true
	}
	changed := upd.Apply(it)
	if changed {
		if err := store.Save(it); err != nil {
			return textContent(encodeErr(5, "ask update: "+err.Error())), true
		}
	}
	body, isErr := encodeJSON(it)
	if isErr {
		return textContent(body), true
	}
	if !changed {
		return []toolContent{
			{Type: "text", Text: body},
			{Type: "text", Text: fmt.Sprintf("ask update: %s already reads as requested", it.ID)},
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
		return nil, "", storeErrCode(err), fmt.Sprintf("ask %s: %s", verb, err.Error()), false
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

// storeErrCode maps a core.OpenStore failure to the §2 exit-code taxonomy
// value carried in the MCP error envelope: 7 when the store is stranded
// (item files present, config.json gone — those asks are real and still
// want a human), 5 for every other open failure including a genuinely
// uninitialized directory (act-55ae5b).
func storeErrCode(err error) int {
	var stranded *core.StrandedStoreError
	if errors.As(err, &stranded) {
		return 7
	}
	return 5
}
