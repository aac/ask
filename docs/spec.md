# ask v1 — Authoritative Implementation Spec

**Status:** v1, written before implementation. Pins everything `docs/brief.md` deferred to spec.

This document is normative. Where it conflicts with `docs/brief.md`, the spec wins (and the brief should be amended). Where it is silent, `docs/brief.md` controls.

Audience: the agents implementing Tasks 2–16 of `docs/plan-v1.md`. Every concrete decision below is a constraint on those tasks, not a suggestion.

---

## 1. JSON output shapes

All JSON output is UTF-8, no BOM. JSON is emitted only when `--json` is passed (or for tools called via MCP, where JSON is the wire format). Without `--json`, output is human-formatted plain text — see §3 for `ask list` columns.

### 1.1 Item object (the canonical shape)

The on-disk schema in `.ask/items/ask-XXXX.json` is the same object emitted by `--json` everywhere. Field order in serialization is the order below. `omitempty` is **not** used: every field is always present, with `null` for unset optionals. This makes downstream parsing trivial (no key-presence branches).

```json
{
  "schema_version":      "1",
  "id":                  "ask-3c89",
  "title":               "Set up Gmail OAuth",
  "body":                "",
  "urgency":             "normal",
  "status":              "open",
  "filed_by":            null,
  "recipient":           null,
  "tracker_ref":         null,
  "verifier":            null,
  "links":               [],
  "blocks":              [],
  "resolution_note":     null,
  "verification_output": null,
  "created_at":          "2026-05-15T10:30:00Z",
  "resolved_at":         null,
  "verified_at":         null,
  "closed_at":           null
}
```

Field constraints:

| Field | Type | Required | Constraints |
|---|---|---|---|
| `schema_version` | string | yes | on-disk schema marker; current value `"1"`; see §1.1.1 |
| `id` | string | yes | `^ask-[0-9a-f]{4}$` |
| `title` | string | yes | 1..200 chars after trim; rejects newlines |
| `body` | string | yes | 0..16384 chars; default `""` |
| `urgency` | string | yes | one of `blocker`, `normal`, `fyi`; default `normal` |
| `status` | string | yes | one of `open`, `resolved`, `closed` |
| `filed_by` | string\|null | no | free-form ≤256 chars; see §10 |
| `recipient` | string\|null | no | free-form 1..1024 chars, non-whitespace; opaque agent-to-agent label (e.g. `agent:data-prep`, `human:andrew`, `team:reviewers`); absent = implicit human ("the user of this .ask/"); see §13 |
| `tracker_ref` | string\|null | no | free-form ≤256 chars |
| `verifier` | object\|null | no | see §1.2 |
| `links` | array | yes | array of `{label,url}` objects; empty array if none; max 32 links; `label` 1..120 chars; `url` 1..2048 chars; URL is not validated beyond non-empty |
| `blocks` | array | yes | array of opaque cross-tool refs (strings) this ask is blocking; empty array if none; each ref 1..1024 chars, non-whitespace (no further format constraint — ask never interprets the value, orchestrators do). Typical refs are `act-XXXX` ids but any string is accepted. |
| `resolution_note` | string\|null | no | ≤16384 chars |
| `verification_output` | string\|null | no | ≤65536 chars; longer values are truncated with a trailing `\n[truncated]\n` marker |
| `created_at` | string | yes | RFC 3339 UTC with `Z` suffix and nanosecond precision when nonzero, otherwise second precision (Go default for `time.Time.MarshalJSON` with UTC) |
| `resolved_at` | string\|null | no | RFC 3339 UTC |
| `verified_at` | string\|null | no | RFC 3339 UTC |
| `closed_at` | string\|null | no | RFC 3339 UTC |

### 1.1.1 `schema_version`

The `schema_version` field on every Item is the **authoritative on-disk
schema marker** for `.ask/items/*.json`. Its current value is the string
`"1"`. Agents and tools that read `.ask/items/` directly (bypassing the
`ask` CLI / MCP) **MUST** check this field before interpreting any other
field — a future schema bump may rename, retype, or restructure any
other field, and reading without checking will silently misinterpret
data.

Rules:

- **Type:** string. Format is opaque; today's value is the single
  character `"1"`. Future values are unconstrained except that they must
  be strings (the field type is fixed). Reserved future shapes include
  `"2"`, `"1.1"`, etc. — code consuming this field MUST do exact string
  equality, not lexicographic or numeric comparison.
- **Write path:** every Item written by `ask` v1 carries
  `schema_version: "1"`. `core.NewItem` stamps it on construction;
  `core.FileStore.Save` defensively re-stamps a zero value before
  marshal so struct-literal callers cannot ship items without the field.
- **Read path (backward compat):** items written before this field
  existed lack the key entirely. The v1 read path treats a missing or
  empty `schema_version` as `"1"` silently and continues — this is the
  one and only legal default. Treat any *explicit* non-empty value other
  than `"1"` as a hard error in v1 (the field exists precisely so a
  future ask version can detect it and either migrate or refuse).
- **No migration framework in v1.** The field is in place so a v1.x
  bump has a hook to migrate from; the framework itself is deferred
  (see `docs/distribution-readiness.md` §6 and §12 below). v1 never
  rewrites existing on-disk items to add the field — read-path tolerance
  is the entire backward-compat story.

### 1.2 Verifier object

```json
{
  "type":            "shell",
  "command":         "pnpm test:gmail-auth",
  "timeout_seconds": 60
}
```

| Field | Type | Required | Constraints |
|---|---|---|---|
| `type` | string | yes | v1 accepts only `"shell"`. `"url"` and `"mcp"` are reserved values and **must be rejected** by `ask new` validation in v1. |
| `command` | string | yes | 1..4096 chars; passed to the user's shell by the agent that runs the verifier (ask itself never executes it in v1) |
| `timeout_seconds` | integer | yes | 1..3600; default 60 when verifier supplied without explicit timeout |

### 1.3 Link object

```json
{ "label": "OAuth console", "url": "https://console.cloud.google.com/..." }
```

### 1.4 `ask list --json`

A JSON array of Item objects, in the order rendered by the human view (§3). Empty array if nothing matches. Never `null`. The list is sorted server-side; clients should not re-sort.

```json
[
  { "id": "ask-3c89", "title": "...", "urgency": "blocker", "status": "open", "...": "..." },
  { "id": "ask-7ecd", "title": "...", "urgency": "normal",  "status": "open", "...": "..." }
]
```

### 1.5 `ask show --json`

A single Item object. If the id is not found, no JSON is emitted; exit code 3 with a stderr message (§2).

### 1.6 `ask new`

- Without `--json`: prints the id followed by `\n` to stdout (e.g. `ask-3c89\n`). Nothing else on stdout.
- With `--json`: prints the full Item object that was just written.

### 1.7 `ask init`

- Without `--json`: prints a one-line summary to stdout, e.g. `initialized .ask/ (project_id=01HXYZ...)`. On re-init: `.ask/ already initialized (project_id=01HXYZ...)`.
- With `--json`: prints `{ "project_id": "...", "display_name": "...", "created_at": "...", "initialized": true|false }`, where `initialized` is `true` only on the first run; `false` for idempotent re-runs.

### 1.8 `ask resolve` / `ask reopen` / `ask close`

- Without `--json`: prints a one-line summary to stdout, e.g. `ask-3c89: open -> resolved`. On idempotent no-op (already in target state): prints `ask-3c89: already resolved` to stdout AND a warning to stderr, exit code 6 (§2).
- With `--json`: prints the post-transition Item object. On idempotent no-op, prints the unchanged Item object and the warning still goes to stderr; exit code 6.

### 1.9 `ask version`

Prints `BinaryVersion` (a single line) to stdout. No `--json` variant in v1.

### 1.10 `ask help [topic]`

Plain text only. No `--json`.

### 1.11 Error envelope (CLI)

CLI errors are always plain text on **stderr**, exit code per §2. Stdout is empty on error (except for `--json` plus successful idempotent no-op, see §1.8). There is no machine-readable CLI error envelope in v1; agents inspect the exit code.

### 1.12 Error envelope (MCP)

See §5.5. MCP tool errors return a structured envelope inside the `tools/call` result with `isError: true`.

---

## 2. Exit codes

The full taxonomy. Every CLI invocation returns exactly one of these.

| Code | Meaning | Examples |
|---|---|---|
| `0` | Success | normal completion of any verb |
| `2` | Validation error | bad flag, missing required arg, title >200 chars, urgency not in set, invalid transition (e.g. `resolve` on a `closed` item), unknown subcommand, unknown help topic, attempt to set `verifier.type` to a reserved-but-unimplemented value (`url`, `mcp`) |
| `3` | Not found | id prefix matches zero items |
| `4` | Ambiguous id prefix | id prefix matches >1 items |
| `5` | I/O / disk error | cannot read/write `.ask/`, corrupt `config.json` (§7), atomic-rename failure, no `.ask/` present when one is required (run `ask init` first) |
| `6` | Already-handled idempotent no-op | `resolve` on a `resolved` item, `close` on a `closed` item, `reopen` on an `open` item, `ask init` in an already-initialized directory when no fields would change. Always paired with a stderr warning. Stdout still emits the success-shape (id or item) so scripts can pipe results uniformly. |

Notes:

- `1` is intentionally unused so that Go's default `exit(1)` from an uncaught panic remains distinguishable from any deliberate exit.
- Concurrent writes that lose a last-write race fall under `5`.
- `--help` / `-h` / `ask help [valid-topic]` always return `0`. An unknown help topic returns `2`.
- Exit code 6 supersedes 0 for idempotent no-ops so that scripts that care can branch on it; scripts that don't care can treat `0` and `6` as equivalent (both leave the system in the desired state).

---

## 3. `ask list` defaults

### 3.1 Default filter

`ask list` with no flags shows items with `status != closed` (i.e. `open` and `resolved`).

### 3.2 Sort order

Sorted by:

1. `urgency` descending: `blocker` (0) before `normal` (1) before `fyi` (2).
2. Within urgency, `created_at` **ascending** (oldest first — the oldest blocker is at the top).
3. Within identical `created_at`, `id` ascending (lexical) as a stable tiebreaker.

This order is the same in plain-text and `--json` output; the JSON array is pre-sorted.

### 3.3 Plain-text columns

Tab-separated columns; no header row (matches `act list` style):

```
<id>\t<urgency>\t<status>\t<verifier-marker>\t<title>
```

Where:

- `<urgency>` is the literal word, lowercased, left-padded to 7 chars (`blocker`/`normal `/`fyi    `).
- `<status>` is the literal word, lowercased, left-padded to 8 chars.
- `<verifier-marker>` is `V` if the item has a verifier attached, ` ` (single space) otherwise.
- `<title>` is the raw title; never truncated. (Agents reading the output should treat the line as `\t`-delimited.)

Empty results: no output on stdout, exit code 0.

### 3.4 Flags

| Flag | Behavior |
|---|---|
| `--status=<state>` | Filter to exact status. Accepts `open`, `resolved`, `closed`. Repeatable: `--status=open --status=resolved` (OR semantics). Mutually exclusive with `--all` is **not** a thing — explicit `--status` overrides the default filter. |
| `--urgency=<u>` | Filter to exact urgency. Accepts `blocker`, `normal`, `fyi`. Repeatable (OR). |
| `--blocks=<ref>` | Filter to items whose `blocks` array contains `ref` (exact match). Repeatable (OR). Composes with `--status` / `--urgency` as AND. Validation: each ref non-empty after trim and 1..1024 chars (matches `ask new --blocks`); a violation exits 2. |
| `--recipient=<ref>` | Filter to items whose `recipient` field equals `ref` (exact match). Repeatable (OR). Composes with the other filters as AND. Items without a recipient (the implicit-human case) never match an explicit `--recipient` filter — there is no sentinel for "implicit human" in v1; callers that want those omit the flag. Validation: each ref non-empty after trim and 1..1024 chars; a violation exits 2. |
| `--all` | Equivalent to no status filter; lists items in every state. Cannot be combined with `--status`. |
| `--json` | Emit JSON array (§1.4). |

Bad enum values are validation errors (exit 2).

---

## 4. ID hash and collision algorithm

### 4.1 Generation

```
input  = project_id || created_at_iso || title
digest = sha256(input)               // 32 bytes
id4    = hex(digest[0:2])            // first 4 hex chars (16 bits)
id     = "ask-" + id4
```

- `project_id` is the ULID from `.ask/config.json` (string form).
- `created_at_iso` is the RFC 3339 string used in the item's `created_at` field, exactly as it will be serialized (UTC, `Z` suffix, nanosecond precision when nonzero).
- `title` is the post-trim title that will be stored.
- The first two bytes of the digest are rendered with `encoding/hex` (lowercase).

### 4.2 Collision handling

Within a single project, the namespace is 2^16 ≈ 65,536 ids. At ≥256 items, birthday-collision probability is non-trivial. The brief deferred a "regenerate `created_at` with +1ns" scheme; the spec adopts it as follows:

1. On `ask new`, after computing the candidate id, the store re-reads `.ask/items/` and checks whether `ask-<id4>.json` already exists.
2. If it exists, increment `created_at` by 1 nanosecond and recompute. Repeat.
3. Hard cap: 1024 retries. On overflow, exit code 5 with stderr message `id collision retries exceeded; .ask/items/ may be at capacity`.
4. The final `created_at` written into the item file matches the value used in the hash that produced the chosen id. Callers do not see the nudged timestamp unless they `ask show` immediately after; the nudge is at most 1µs in practice.

Concurrent writers: the directory re-read is the only collision check. Two simultaneous `ask new` calls on different processes can race and produce the same id; the second `os.Rename` will overwrite the first. This is documented as out of scope for v1 (single human, single machine, single agent at a time in any given project — same v1 audience as the brief's §Concurrent safety). Reconsider if dogfooding shows the race.

### 4.3 Prefix lookup

Implements act's prefix-resolution policy:

- Input: any non-empty hex prefix, with or without the `ask-` prefix (`ask show ask-3c`, `ask show 3c`, `ask show ASK-3C` all equivalent).
- Resolution: case-insensitive; normalize to lowercase, strip `ask-` if present, glob `.ask/items/ask-<prefix>*.json`.
- 0 matches → exit 3.
- 1 match → operate on that id.
- >1 matches → exit 4; stderr lists the ambiguous ids (newline-separated).
- A full 4-char id with no match still exits 3 (not 4); a full 4-char id that exists matches itself.

---

## 5. MCP tool schemas

`ask mcp` exposes one tool per CLI verb. Wire protocol: JSON-RPC 2.0 over stdio, MCP `2024-11-05`, server name `ask-mcp`, server version matches `BinaryVersion`. Each tool's `inputSchema` is a JSON Schema (draft 2020-12, but only using draft-7-compatible features) declaring the same fields as the CLI flags. Each tool's success response is the corresponding `--json` output (§1) wrapped in the MCP `content` envelope.

### 5.1 Common conventions

- Tool names: `ask_new`, `ask_list`, `ask_show`, `ask_resolve`, `ask_reopen`, `ask_close`. (`ask_init` is **not** exposed via MCP in v1 — initialization is a developer action, not an agent action.)
- Required vs optional in each schema mirrors CLI requiredness.
- Response framing: every tool returns `{ "content": [{ "type": "text", "text": "<json>" }], "isError": false }` where `<json>` is the same payload the CLI emits with `--json`. The JSON-encoded payload is in the `text` field (single-string convention; agents JSON-parse the `text`).

### 5.2 `ask_new`

`inputSchema`:

```json
{
  "type": "object",
  "required": ["title"],
  "additionalProperties": false,
  "properties": {
    "title":       { "type": "string", "minLength": 1, "maxLength": 200 },
    "body":        { "type": "string", "maxLength": 16384 },
    "urgency":     { "type": "string", "enum": ["blocker", "normal", "fyi"] },
    "filed_by":    { "type": "string", "maxLength": 256 },
    "recipient":   { "type": "string", "minLength": 1, "maxLength": 1024 },
    "tracker_ref": { "type": "string", "maxLength": 256 },
    "verifier": {
      "type": "object",
      "required": ["command"],
      "additionalProperties": false,
      "properties": {
        "type":            { "type": "string", "enum": ["shell"] },
        "command":         { "type": "string", "minLength": 1, "maxLength": 4096 },
        "timeout_seconds": { "type": "integer", "minimum": 1, "maximum": 3600 }
      }
    },
    "links": {
      "type": "array",
      "maxItems": 32,
      "items": {
        "type": "object",
        "required": ["label", "url"],
        "additionalProperties": false,
        "properties": {
          "label": { "type": "string", "minLength": 1, "maxLength": 120 },
          "url":   { "type": "string", "minLength": 1, "maxLength": 2048 }
        }
      }
    },
    "blocks": {
      "type": "array",
      "items": { "type": "string", "minLength": 1, "maxLength": 1024 }
    }
  }
}
```

Return: the created Item object (§1.1).

`blocks` validation: in addition to the schema's minLength/maxLength,
the handler rejects refs that are whitespace-only after trim (code 2).

`recipient` validation: when present, the value must be 1..1024 chars
and non-whitespace after trim — same shape as a single `blocks` ref.
Whitespace-only values are rejected (code 2). The field is opaque; ask
never interprets the format (see §13).

### 5.3 `ask_list`

`inputSchema`:

```json
{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "status": {
      "type": "array",
      "items": { "type": "string", "enum": ["open", "resolved", "closed"] }
    },
    "urgency": {
      "type": "array",
      "items": { "type": "string", "enum": ["blocker", "normal", "fyi"] }
    },
    "blocks":    { "type": "string", "minLength": 1, "maxLength": 1024 },
    "recipient": { "type": "string", "minLength": 1, "maxLength": 1024 },
    "all":       { "type": "boolean" }
  }
}
```

The `blocks` filter is a single string (mirrors the CLI per-flag shape
flattened to one ref per call: orchestrators typically know the one
ref they're coordinating). Items match when their `blocks` array
contains the ref (exact match). Composes with `status`/`urgency` as
AND.

The `recipient` filter has the same shape and conventions: a single
string ref, exact match against the item's `recipient` field. Items
without a recipient set never match an explicit filter — the
implicit-human case is queried by omitting `recipient` and
post-filtering on the client. Composes with the other filters as AND.

Defaults match the CLI: omitted `status` and no `all` → items with `status != closed`. `all: true` plus a `status` value is a validation error.

Return: an array of Item objects (§1.4).

### 5.4 `ask_show`, `ask_resolve`, `ask_reopen`, `ask_close`

All four take `id` as the required arg. `resolve` accepts an optional `note`; `reopen` and `close` accept an optional `reason`.

```json
{
  "type": "object",
  "required": ["id"],
  "additionalProperties": false,
  "properties": {
    "id":     { "type": "string", "minLength": 1 },
    "note":   { "type": "string", "maxLength": 16384 },
    "reason": { "type": "string", "maxLength": 16384 }
  }
}
```

(`note` is meaningful only for `ask_resolve`; `reason` is meaningful only for `ask_reopen` and `ask_close`. Schemas omit the irrelevant field on a per-tool basis — `ask_show` accepts only `id`.)

Return: the post-transition (or current, for `ask_show`) Item object (§1.5 / §1.8).

### 5.5 MCP error envelope

On any non-success outcome, the tool returns:

```json
{
  "content": [
    { "type": "text", "text": "{\"code\": 3, \"message\": \"ask-9999: not found\"}" }
  ],
  "isError": true
}
```

- `code` is the exit-code taxonomy value (§2): 2/3/4/5/6.
- `message` is the same human-readable string the CLI would have printed to stderr.
- Idempotent no-ops (code 6) are returned as `isError: false` with the item payload **and** an additional `content` text element containing the warning, mirroring the CLI's stdout-success-plus-stderr-warning pattern. (Justification: an MCP-side warning that arrives as `isError: true` is the wrong affordance — the call succeeded.) Concretely:
  - The `content` array has exactly two entries, both `type: "text"`.
  - `content[0].text` is the JSON-encoded post-call Item (identical to the active-transition success body), so clients parsing `content[0]` always get an Item regardless of whether the call was a real transition or a no-op.
  - `content[1].text` is the verbatim warning string `ask <verb>: already <status>`, matching the CLI's stderr line and serving as the structured no-op signal an orchestrator/verifier can match on without diffing item state.
  - Example for `ask_resolve` on an already-resolved item: `content[1].text == "ask resolve: already resolved"`.

JSON-RPC framing errors (malformed input, unknown method) are returned as JSON-RPC `error` objects per the spec (`-32600` etc.), not via the tool envelope.

---

## 6. `ask init` idempotency

`ask init` is safe to run repeatedly. Behavior:

1. **`.ask/` does not exist:**
   - Create `.ask/` and `.ask/items/`.
   - Generate `project_id` (ULID; uppercase Crockford base32, 26 chars).
   - Write `.ask/config.json` with `project_id`, `display_name` (default: basename of `cwd`; override with `--name`), `created_at` (current UTC time, RFC 3339).
   - If a `.gitignore` exists in `cwd`: append `.ask/` on a new line if not already present (substring match on lines: an existing `.ask/` or `.ask` or `.ask/**` line counts as present).
   - If no `.gitignore`: create one containing `.ask/\n` only when the directory is inside a git repository (detected by walking up to find a `.git` directory or file). Outside a git repo, do nothing for `.gitignore`.
   - Exit 0. `initialized: true`.

2. **`.ask/` exists and `config.json` is valid:**
   - Preserve `project_id` and `created_at` verbatim.
   - If `--name` is given and differs from the existing `display_name`, update `display_name` and rewrite `config.json` via atomic rename. Output reflects the new value. Exit 0.
   - If `--name` is absent or matches the existing `display_name`, no file is rewritten. Exit 6 (idempotent no-op) and stderr warning `ask init: already initialized`.
   - Gitignore step is re-run idempotently (append only if missing).

3. **`.ask/` exists but `config.json` is missing or corrupt:**
   - Exit 5 (§7). `ask init` does **not** auto-repair in v1.

4. **`.ask/items/` exists but `.ask/` directory itself is missing:** impossible (items are under `.ask/`). If `.ask/items/` is missing but `.ask/` exists, `ask init` recreates `.ask/items/` as part of the idempotent path.

### 6.1 `--name` flag

`--name=<display_name>` overrides the default basename, both on first init and on re-init (the only field re-init mutates).

### 6.2 Flags reserved

No other flags in v1. `--force` is **not** implemented (no destructive reset path).

---

## 7. `.ask/config.json` corruption

Detected conditions:

- File does not exist when one is required (any verb other than `ask init`, `ask version`, `ask help`).
- File exists but is not valid UTF-8 JSON.
- File parses as JSON but does not contain the required keys (`project_id`, `display_name`, `created_at`) with the right types.
- `project_id` does not match `^[0-9A-HJKMNP-TV-Z]{26}$` (Crockford base32 ULID grammar).

Behavior:

- Exit code 5.
- Stderr message names the file and the failure reason, e.g.:
  - `ask: cannot read .ask/config.json: no such file or directory (run 'ask init')`
  - `ask: .ask/config.json is invalid JSON: unexpected EOF`
  - `ask: .ask/config.json missing required field "project_id"`
  - `ask: .ask/config.json has invalid project_id (expected ULID)`
- No file is written. No item is touched. No auto-recovery is attempted in v1.

Item files (`.ask/items/ask-XXXX.json`) follow the same policy individually: a corrupt item file makes that one id unreadable (`ask show ask-XXXX` → exit 5, `ask list` skips it and emits a stderr warning per skipped id but continues with the rest, exit 0 if at least one item was readable, exit 5 only if every requested item was unreadable). The skip-and-warn behavior is the only deviation from "no auto-recovery"; it is necessary so that one bad file does not lock the entire inbox.

---

## 8. Atomic rename on Windows

Every mutation to `.ask/config.json` or any `.ask/items/ask-XXXX.json` uses the same pattern:

1. Marshal JSON.
2. Write to `<target>.tmp` in the same directory (same-volume guarantee for rename atomicity).
3. `os.Rename(<target>.tmp, <target>)`.

`os.Rename` on Windows is atomic for same-volume renames as of Go 1.5+, including rename-over-existing files (Go uses `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING`). This is the inherited semantics ask relies on. The spec does **not** wrap rename with any retry or fallback in v1.

Edge cases:

- `<target>.tmp` already exists from a prior crashed write: `os.Rename` overwrites it during the write step (Go's `os.OpenFile` with `O_TRUNC` clobbers the partial file). No staleness handling needed.
- Antivirus or file-indexers holding a transient handle on Windows can fail `os.Rename` with a "process cannot access the file" error: this surfaces as exit 5 with the OS error verbatim. v1 does not retry; if it becomes a real issue in dogfooding, add a short bounded retry loop in core/store.
- The `.tmp` files are written under the same directory as the target so `os.Rename` never crosses volumes.

---

## 9. Verifier env inheritance

When an agent (not `ask` itself, in v1) runs a verifier:

- **Working directory:** the directory containing `.ask/`. The agent is responsible for `cd`-ing or equivalent.
- **Environment:** the full calling process environment, including `PATH`. No allowlist, no scrubbing.
- **Timeout:** `verifier.timeout_seconds` is advisory; ask does not enforce it. The agent honors it (typically via `context.WithTimeout` or shell `timeout`).
- **Capture:** stdout and stderr both captured, concatenated in chronological order if possible (else stdout then stderr), then trimmed to ≤65536 chars for storage in `verification_output` (§1.1).

Rationale for no allowlist: verifiers are user-authored commands that typically rely on the user's shell environment (PATH, NVM, ASDF, virtualenv activations, AWS_PROFILE, etc.). Scrubbing them would make most useful verifiers fail. Since `ask` does not execute the verifier itself in v1, this is also a description of what agents should do, not what `ask` enforces.

The `ask help verifier` topic must include this paragraph verbatim or close to it.

---

## 10. `filed_by` convention

`filed_by` is a free-form, optional string ≤256 chars. It is metadata only; ask never parses or branches on it.

**Recommended (non-binding) format:** `<source>:<session-or-id>`.

Examples:

- `claude-code:session-abc123`
- `claude-code:7f3a1b9e` (truncated session id)
- `aider:run-2026-05-15T10:30Z`
- `cron:nightly-verifier`
- `human` (acceptable when a human runs the CLI directly)

Conventions for agent implementers:

- If the calling agent has a stable session identifier, use it.
- If not, omit `filed_by` rather than inventing one.
- Do not put PII or secrets in this field.
- Tools that aggregate across projects (out of scope for v1) may use this field for grouping; do not over-promise stability.

The `ask new` flag is `--filed-by=<string>`. The MCP arg is `filed_by`.

---

## 13. `recipient` convention

`recipient` is a free-form, optional string. It tags who should pick the
ask up — the audience the filing agent intends to handle it. ask never
parses or branches on the value; orchestrators do.

**Type and validation:** `string | null`, 1..1024 chars, non-whitespace
after trim. Absent (`null`) means "no recipient declared — the implicit
human (the user of this `.ask/`)." Validation matches a single `blocks`
ref (§1.1).

**Recommended (non-binding) format:** `<kind>:<identifier>`.

Examples:

- `agent:data-prep` — an agent that polls its inbox on cron
- `agent:reviewer-bot` — a sibling agent process
- `human:andrew` — an explicit human label
- `team:reviewers` — a group label an orchestrator interprets

Conventions for agent implementers:

- Set `recipient` when filing an ask that the implicit human is *not*
  expected to pick up — typically because the consumer is another agent
  (a peer process, a cron-driven loop, a sibling worktree run). Omit
  when the ask is for the implicit human.
- The kind prefix (`agent:`, `human:`, `team:`) is a convention only.
  ask treats the entire value as opaque. Orchestrators that filter by
  prefix to scope their poll do so at their own discretion.
- Do not put PII or secrets in this field.

**v1 deferrals (out of scope):** agent identity registry, authorization,
routing/delivery semantics, multi-recipient fan-out. The field is a
*label* — receivers poll their own inboxes; nothing in ask delivers,
authorizes, or routes. See `docs/design-ask-affordance-and-multi-recipient.md`
§Q2 for the design rationale.

The `ask new` flag is `--recipient=<string>`. The MCP arg is
`recipient`. The `ask list` filter is `--recipient=<string>` (CLI,
repeatable OR) and `recipient` (MCP, single string).

---

## 11. Cross-check against brief

This section confirms every spec-deferred item the brief mentions is resolved above. Each row references the brief section that punted and the spec section that lands the decision.

| Brief reference | Resolution |
|---|---|
| §Data model: `verifier.type` reserves `url`/`mcp` | §1.2 — rejected at validation in v1 (exit 2) |
| §Data model: `verifier.timeout_seconds` is advisory | §9 — agents enforce, ask does not |
| §Data model: status field as resume cursor | §3.1 default filter + §5.3 list defaults preserve this |
| §ID format: 4-char hex, collision rare | §4.1 / §4.2 — sha256+truncate, retry-with-+1ns, cap 1024 |
| §ID format: prefix lookup, ambiguity errors | §4.3 — case-insensitive, exit 4 on >1 match |
| §Lifecycle: `resolution_note` preservation | §1.1 (always present, set per transition rules in brief) |
| §Lifecycle: `verification_output` truncation | §1.1 — 65536 char cap with marker |
| §Verification: cwd = `.ask/` parent | §9 |
| §Verification: env inheritance | §9 — full env, no allowlist |
| §Concurrent safety: atomic rename via `<x>.tmp` | §8 — `os.Rename`, Windows semantics inherited |
| §Concurrent safety: idempotent verbs | §1.8 + §2 — exit 6 + stderr warning |
| §CLI surface: `--json` on every relevant verb | §1.1–§1.10 |
| §CLI surface: `ask init` gitignore handling | §6.1 — append if missing, create only inside git repo |
| §CLI surface: prefix resolution | §4.3 |
| §Help command: topic surface | §1.10 + brief §Help unchanged |
| §Skill: orchestrator checkpoints | brief §Filing unchanged; spec adds nothing |
| §Out of scope items | unchanged from brief |
| (new) Exit code taxonomy | §2 |
| (new) MCP tool schemas / error envelope | §5 |
| (new) config.json corruption | §7 |
| (new) `filed_by` convention | §10 |
| (new) `recipient` field (agent-to-agent label) | §13 |

**v1: deferred items** (intentionally not pinned by this spec; tracked for post-v1):

- Concurrent-resume safety (two agents picking up the same `resolved` item). Documented out of scope in brief §Concurrent safety; revisit if dogfooding surfaces real loss.
- Concurrent `ask new` from two processes colliding on id. Documented in §4.2.
- Verifier execution by `ask` itself (`ask verify <id>` verb). Not in v1 surface; would be additive.
- Aggregated cross-project inbox, TUI, web UI, push notifications, custom item types. Listed in brief §Out of scope; spec adds nothing.

---

## 12. Forward-compatibility constraints

To keep post-v1 changes additive rather than breaking:

- **Item schema:** new fields must default to `null` or `[]` and must not change the meaning of existing fields. v1 parsers should ignore unknown fields on read (`json.Decoder` without `DisallowUnknownFields`). The `schema_version` field (§1.1.1) is the migration hook for any change that *can't* be expressed as a pure additive field — bump it and treat the old value as legacy on the read path.
- **`config.json`:** same. New fields default to absent-tolerant values.
- **Exit codes:** the taxonomy in §2 is closed; new error classes get new codes (`7`, `8`, ...) rather than reusing existing ones with new semantics.
- **MCP tools:** new tools are additive. Existing tools' `inputSchema` may grow optional properties; required properties cannot change. Response shape changes are breaking and require a wire-version bump.
- **CLI flags:** new flags must be optional. Renaming an existing flag is breaking and requires a major-version bump.

---

End of spec.
