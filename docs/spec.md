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
| `title` | string | yes | 1..200 chars after trim, counted in characters (Unicode code points), not bytes; rejects newlines. Enforced by the CLI and the MCP server alike |
| `body` | string | yes | default `""`; ≤16384 chars per MCP argument, unbounded through the CLI (see the note below the table) |
| `urgency` | string | yes | one of `blocker`, `normal`, `fyi`; default `normal` |
| `status` | string | yes | one of `open`, `resolved`, `closed` |
| `filed_by` | string\|null | no | free-form; ≤256 chars per MCP argument; see §10 |
| `recipient` | string\|null | no | free-form 1..1024 chars (Unicode code points), non-whitespace; opaque agent-to-agent label (e.g. `agent:data-prep`, `human:andrew`, `team:reviewers`); absent = implicit human ("the user of this .ask/"); see §13 |
| `tracker_ref` | string\|null | no | free-form; ≤256 chars per MCP argument |
| `verifier` | object\|null | no | see §1.2 |
| `links` | array | yes | array of `{label,url}` objects; empty array if none; max 32 links; `label` 1..120 chars; `url` 1..2048 chars; URL is not validated beyond non-empty |
| `blocks` | array | yes | array of opaque cross-tool refs (strings) this ask is blocking; empty array if none; each ref 1..1024 chars (Unicode code points), non-whitespace (no further format constraint — ask never interprets the value, orchestrators do). Typical refs are `act-XXXX` ids but any string is accepted. |
| `resolution_note` | string\|null | no | ≤16384 chars per MCP argument |
| `verification_output` | string\|null | no | ≤65536 chars; longer values are truncated with a trailing `\n[truncated]\n` marker |
| `created_at` | string | yes | RFC 3339 UTC with `Z` suffix and nanosecond precision when nonzero, otherwise second precision (Go default for `time.Time.MarshalJSON` with UTC) |
| `resolved_at` | string\|null | no | RFC 3339 UTC |
| `verified_at` | string\|null | no | RFC 3339 UTC |
| `closed_at` | string\|null | no | RFC 3339 UTC |

**Which limits bind where.** The CLI enforces the `title` limit and the 1..1024 limit on each `blocks` ref and on `recipient`. Those three are counted in characters (Unicode code points), not bytes, and `ask new` and `ask_new` apply them through the same validators, so 1024 characters in any script is accepted on both and 1025 is refused on both (exit / code 2, `blocks: ref exceeds 1024 characters`, nothing written). The `--blocks` / `--recipient` filters of `ask list` and the `blocks` / `recipient` filters of `ask_list` use that validator too, so any ref that can be stored can be filtered for. The CLI applies no length cap to the free-text fields: `body`, `filed_by`, `tracker_ref`, the verifier command, and the note or reason behind `resolution_note`. Those caps are limits of the MCP boundary (§5.1), applied to each argument a client sends, and are not a property of a stored item: an item filed or edited through the CLI may hold a longer value, and every reader must accept one. The CLI is left unbounded on purpose. Agents do file long bodies (two of 789 live items measured on 2026-10-12 had bodies over 16384 characters, the longest 24388), and an ask refused at filing time is the worst failure this tool can have: the item that needed a human is simply lost.

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
  Concretely: the item is unreadable to this binary, on the same
  per-item footing as a corrupt item file (§7.2). `ask show` and every
  verb that would rewrite the item (`resolve`, `close`, `reopen`,
  `update`, `harvest`, and their MCP twins) fail with exit 5 and a
  message naming `schema_version` and the value found; the file is left
  byte-identical, and the item is never listed as an ordinary v1 item.
  The check runs before any other field is interpreted.
  `harvest` is all-or-nothing about this: it reads and validates every
  source item before writing any, so if one or more are unreadable
  (non-v1 or corrupt) it exits 5 naming each such id, having copied
  nothing into the target and — `--clean` or not — removed nothing from
  the source. It never skips an unreadable item. When the same run also
  has an id collision with the target, the one message names both the
  colliding ids and every unreadable id.
  The same refusal covers the target side of a case clash: if the
  target's `items/` holds an entry whose name matches a source item's
  file name (`<id>.json`) case-insensitively without being exactly that
  name (`ASK-AAAB.json`, `ask-aaab.JSON` against `ask-aaab`), `harvest`
  exits 5 naming the file and the source id, copying and removing
  nothing. On a case-insensitive filesystem the copy would otherwise be
  written into that entry and keep its name, leaving the harvested ask
  unreadable. The check compares names and does not probe the
  filesystem, so the store is refused on a case-sensitive one too. It is
  reported in the same message as a collision or unreadable items.
  The target's `subs/` is checked the same way against the subs file
  (`subs/<id>.json`) of every source item that has subscriptions, the
  only subs files a harvest writes; such an entry is named with its
  directory (`subs/ASK-AAAB.json`) in that same message. A source item
  with no subscriptions is not checked against `subs/`.
  A store's own `subs/` gets the same care outside `harvest`: when it
  holds no `<id>.json` for an item but does hold an entry matching that
  name case-insensitively (`subs/ASK-AAAB.json` against `ask-aaab`),
  every verb that reads or writes the item's subscriptions (`subscribe`,
  `unsubscribe`, the text form of `show`) exits 5 naming the file and the
  id, reads nothing from it and changes nothing. A status change
  (`resolve`, `close`, `reopen`) still goes through and fires nothing,
  as with any unreadable subs file. When `<id>.json` is there too, which
  only a case-sensitive filesystem allows, it is the item's subs file
  and the other entry is left alone, as a stray in `items/` is beside a
  real item (§4.3). The check compares names, so it is the same on
  either kind of filesystem. `ask show --json` is the item alone and
  does not read `subs/`.
- **Write path refusal:** `core.FileStore.Save` refuses to write an Item
  whose `schema_version` is anything other than `"1"`, so no code path
  can produce or overwrite a non-v1 item file.
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
| `command` | string | yes | 1..4096 chars; passed to the user's shell by the agent that runs the verifier (ask itself never executes it in v1; the only thing ask executes is a subscribed handler, §14) |
| `timeout_seconds` | integer | yes | 1..3600; default 60 when verifier supplied without explicit timeout |

The default is applied when the item is created: `ask new --verifier <cmd>` with no `--timeout` and `ask_new` with no `verifier.timeout_seconds` both store `60`. A timeout that is given must be in range on both surfaces: `ask new --verifier <cmd> --timeout <n>` with `n` outside 1..3600 exits `2` with `ask new: --timeout must be between 1 and 3600 (got <n>)` and writes nothing, as `ask_new` refuses the same `verifier.timeout_seconds` (§5.1). An explicit `0` is out of range like any other value; it does not mean "default" — omit the flag (or the argument) for that. `--timeout` without `--verifier` has nothing to apply to and is ignored, unchecked. Item files written before that was so hold `0`. They are still valid: every read (`show`, `list`, the MCP tools) reports a stored `0` as `60`, so a caller never sees `0` and needs no branch for it. A read does not rewrite the file; the stored value becomes `60` the next time the item is written.

### 1.3 Link object

```json
{ "label": "OAuth console", "url": "https://console.cloud.google.com/..." }
```

### 1.4 `ask list --json`

A JSON array of Item objects, in the order rendered by the human view (§3). Empty array if nothing matches. Never `null`. Unreadable item files are left out of the array and warned about on stderr (§7.2); the array's shape does not change when that happens, and a consumer that must know the array is complete passes `--strict` and reads the exit code (§7.2). The list is sorted server-side; clients should not re-sort.

```json
[
  { "id": "ask-3c89", "title": "...", "urgency": "blocker", "status": "open", "...": "..." },
  { "id": "ask-7ecd", "title": "...", "urgency": "normal",  "status": "open", "...": "..." }
]
```

### 1.5 `ask show --json`

A single Item object. If the id is not found, no JSON is emitted; exit code 3 with a stderr message (§2). The object is the item alone: subscriptions and fires are not part of it, and `ask show --json` reads neither `subs/` nor `fires/`. The text form of `ask show` adds a `Subscriptions:` section for an item that has any (§14.6).

### 1.6 `ask new`

- Without `--json`: prints the id followed by `\n` to stdout (e.g. `ask-3c89\n`). Nothing else on stdout.
- With `--json`: prints the full Item object that was just written.
- Relative-date warning: when the title or body contains relative-date language (`today`, `tonight`, `tomorrow`, `yesterday`, `this morning`, `last night`, `this week`, `next week`, `in N days`, `N days ago`, or a weekday name with no date within a few characters of it), one line per phrase goes to stderr, prefixed `ask new: warning:`, naming the absolute date the phrase resolves to at filing time in the filer's local zone. The item is still filed, exit 0; stdout is unchanged. Recurring days (`every Monday`, `Sundays`) do not warn.
- Act-dep follow-up: `--blocks` records the edge on the ask side only, and `act ready` keeps listing the act issue until it carries the reciprocal external dep. So for each `blocks` ref shaped like an act id (`act-` followed by 4..32 lowercase hex digits, e.g. `act-3c89`), `ask new` writes one line to stderr, in `--blocks` order with repeats dropped, naming the command that completes the handshake: `ask new: act ready ignores --blocks until the act issue carries the dep; run: act dep add <act-id> --external <new-ask-id>`. The command is everything after `run: `, and the act-id shape guarantees it is safe to paste into a shell. A ref of any other shape gets no line (ask still stores it verbatim; the shape decides only the hint). This applies with and without `--json`: stdout is unchanged, the item is filed, exit 0. `ask_new` carries the same line as an extra content part (§5.2).

### 1.7 `ask init`

- Without `--json`: prints a one-line summary to stdout, e.g. `initialized .ask/ (project_id=01HXYZ...)`. On re-init: `.ask/ already initialized (project_id=01HXYZ...)`.
- With `--json`: prints `{ "project_id": "...", "display_name": "...", "created_at": "...", "initialized": true|false }`, where `initialized` is `true` only on the first run; `false` for idempotent re-runs.

### 1.8 `ask resolve` / `ask reopen` / `ask close`

- Without `--json`: prints a one-line summary to stdout, e.g. `ask-3c89: open -> resolved`. On idempotent no-op (already in target state): prints `ask-3c89: already resolved` to stdout AND a warning to stderr, exit code 6 (§2).
- With `--json`: prints the post-transition Item object. On idempotent no-op, prints the unchanged Item object and the warning still goes to stderr; exit code 6.

### 1.8.1 `ask update`

`ask update <id>` corrects an item's `title`, `body`, and/or `verifier` in place. It makes **no** state transition and stamps no lifecycle timestamps: an ask that still needs a human stays `open` while its wording is fixed. It is allowed on items in any status — correcting the record of something already closed is not a state change.

Flags (the body flags are mutually exclusive, as are the two verifier flags; supplying more than one of a group is a validation error, exit 2):

| Flag | Meaning |
|---|---|
| `--title=<text>` | Replace the title. Same validation as `ask new`: non-empty after trim, 1..200 characters (not bytes), no newlines. Leading/trailing whitespace is trimmed. |
| `--body=<text>` | Replace the body. An explicitly supplied empty string (`--body ""`) clears it; an omitted flag leaves it alone. |
| `--body-file=<path>` | Replace the body with the UTF-8 contents of `path`. `-` reads stdin. |
| `--body-append=<text>` | Append to the existing body, separated by a blank line. An empty existing body yields the addition alone (no leading blank line). |
| `--body-append-file=<path>` | As `--body-append`, reading the text from `path` (`-` = stdin). |
| `--verifier=<cmd>` | Replace the verifier's shell command. An existing verifier keeps its `type` and `timeout_seconds`; an item with no verifier gains one (`type: "shell"`, `timeout_seconds: 60`), as `ask new --verifier` would have given it. An empty or whitespace-only command is a validation error (exit 2) — removing the verifier is `--verifier-clear`. |
| `--verifier-clear` | Remove the verifier (`verifier` becomes `null`). |

Names mirror `act update` (`--description` / `--description-append` / `--description-file`) so the sibling tools stay learnable together; ask's field is `body`, so the flags follow the field.

Invoking with an id but no field flag is a validation error (exit 2) — `ask update` never has a "do nothing" success.

- Without `--json`: prints `ask-3c89: updated` to stdout.
- With `--json`: prints the updated Item object.
- Idempotent no-op: when the requested text is already exactly what is on disk, no write happens (the item's mtime is untouched), stdout still emits the success shape with `unchanged` in place of `updated`, a warning goes to stderr, and the exit code is 6 (§2) — the same envelope as §1.8.

### 1.9 `ask version`

Prints the binary version (a single line) to stdout. No `--json` variant in v1.

- A release build prints the `-ldflags` stamp (`internal/version.Binary`), e.g. `v0.3.0+54537ac`.
- An unstamped build from a git checkout (plain `go build` / `go install ./cmd/ask`) prints `dev+<12-char commit>[.dirty] <commit time>`, e.g. `dev+54537ac0c321 2026-08-11T13:00:50Z`, from the build info the Go toolchain embeds, so two builds of different commits never print the same string.
- `go install github.com/aac/ask/cmd/ask@vX.Y.Z` prints the module version.
- Only a build with no VCS information at all (e.g. `-buildvcs=false`) prints the bare `dev`.

The MCP `initialize` response's `serverInfo.version` reports the same string.

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
| `2` | Validation error | bad flag, missing required arg, title >200 chars, urgency not in set, invalid transition (e.g. `resolve` on a `closed` item), unknown subcommand, unknown help topic, attempt to set `verifier.type` to a reserved-but-unimplemented value (`url`, `mcp`), `ask subscribe` on a `closed` item or with a handler that is not installed (§14.2) |
| `3` | Not found | id prefix matches zero items |
| `4` | Ambiguous id prefix | id prefix matches >1 items |
| `5` | I/O / disk error | cannot read/write `.ask/`, corrupt `config.json` (§7), an item file that is corrupt or carries a `schema_version` other than `"1"` (§1.1.1) when a verb addresses that item (`ask list` skips it with a warning and exits `5` only if no item was readable, or if `--strict` was given and any item was skipped, §7.2), atomic-rename failure, no `.ask/` present when one is required (run `ask init` first) |
| `6` | Already-handled idempotent no-op | `resolve` on a `resolved` item, `close` on a `closed` item, `reopen` on an `open` item, `ask init` in an already-initialized directory when no fields would change, `ask subscribe` when an identical subscription is already stored. Always paired with a stderr warning. Stdout still emits the success-shape (id or item) so scripts can pipe results uniformly. |
| `8` | Stored, but the event it waits for has already happened | `ask subscribe` on an item that is already `resolved`, with a subscription that names `resolve` (§14.2). The subscription is stored and a stderr line says so; it fires only if the item is reopened and resolved again. Distinct from `0` so a caller that subscribed in order to be told of the resolve learns it has to look at the item now. Only `ask subscribe` returns it. |
| `7` | Stranded store | `.ask/config.json` is absent but `.ask/items/` holds item files (§7). Distinct from `5` so a consumer can tell "no inbox here" from "an unreachable inbox holding N asks" without inspecting `.ask/items/` itself. `ask init` in that directory is the repair; it adopts the existing items. |

Notes:

- `1` is intentionally unused so that Go's default `exit(1)` from an uncaught panic remains distinguishable from any deliberate exit.
- Writers of one store are serialized by the store lock (§8). A writer that cannot take the lock within 10 s exits `5` with `ask <verb>: store is locked by another ask process (waited 10s); nothing was written` on stderr; the MCP tools return the same message with code `5`. Nothing has been written when that happens, so the call can be retried as is.
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
| `--strict` | Exit `5` if any item file was unreadable and skipped. Stdout and the per-id stderr warnings are the same as without the flag; with nothing skipped the flag changes nothing. The way a consumer that reads only stdout and the exit code detects a partial list (§7.2). |

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

Concurrent writers: the directory re-read is the only collision check, and `ask new` does not take the store lock. Two simultaneous `ask new` calls on different processes can race and produce the same id; the second `os.Rename` will overwrite the first. This is still out of scope. Writers of an existing item or of its subscriptions (`resolve`, `reopen`, `close`, `update`, `harvest`, `subscribe`, `unsubscribe`) are a different case: they are serialized by the store lock (§8), so any number of agents may write to one store at once. Reconsider the `ask new` race if dogfooding shows it.

### 4.3 Prefix lookup

Implements act's prefix-resolution policy:

- Input: any non-empty hex prefix, with or without the `ask-` prefix (`ask show ask-3c`, `ask show 3c`, `ask show ASK-3C` all equivalent).
- Resolution: case-insensitive; normalize to lowercase, strip `ask-` if present, glob `.ask/items/ask-<prefix>*.json`.
- 0 matches → exit 3.
- 1 match → operate on that id.
- >1 matches → exit 4; stderr lists the ambiguous ids (newline-separated).
- A full 4-char id with no match still exits 3 (not 4); a full 4-char id that exists matches itself.
- Only files named for a valid id (§1.1, `ask-<4 lowercase hex>.json`) are candidates. A stray file in `items/` whose name is not one (`ASK-AAAB.json`) never makes a prefix ambiguous and never stands in for the item whose name it resembles. If a prefix matches no item but would match such a file, the verb exits 5 (not 3) and stderr names the file.

---

## 5. MCP tool schemas

`ask mcp` exposes seven tools: one for each verb an agent uses on an item in the middle of its own loop (`new`, `list`, `show`, `update`, `resolve`, `reopen`, `close`). The verbs that set up or administer a store or its callbacks (`init`, `harvest`, `subscribe`, `unsubscribe`, `fires`) are CLI-only, as are `mcp`, `help` and `version`. A status change made through a tool fires the item's subscriptions exactly as the CLI verb does (§14.4); the tool's result is unchanged by it. Wire protocol: JSON-RPC 2.0 over stdio, MCP `2024-11-05`, server name `ask-mcp`, server version matches `BinaryVersion`. Each tool's `inputSchema` is a JSON Schema (draft 2020-12, but only using draft-7-compatible features) declaring the same fields as the CLI flags. Each tool's success response is the corresponding `--json` output (§1) wrapped in the MCP `content` envelope.

### 5.1 Common conventions

- Tool names: `ask_new`, `ask_list`, `ask_show`, `ask_update`, `ask_resolve`, `ask_reopen`, `ask_close`. (`init`, `harvest`, `subscribe`, `unsubscribe` and `fires` are **not** exposed via MCP — setting up or administering a store, and wiring or inspecting its callbacks, is a developer action, not something an agent does to an item mid-loop.)
- Required vs optional in each schema mirrors CLI requiredness.
- Every `maxLength` a schema advertises is enforced by the server, not left to the client: a longer value is a validation error (code 2, §5.5) whose message names the field and the limit, e.g. `ask new: body exceeds 16384 characters (got 16385)`, and nothing is written. Length is counted in characters (Unicode code points), as JSON Schema defines `maxLength`. These are limits of the MCP boundary, applied to each argument a call sends and never to what is already stored; the CLI does not apply the free-text caps (§1.1 says which limits it does apply, and why the rest are left off). The `title` limit is the exception that both surfaces share, counted the same way.
- One limit bounds a stored result rather than an argument: `ask_update` refuses a `body_append` that would leave the stored body longer than 1048576 characters (§5.4.1). Per-argument caps alone would let a client grow an item forever one within-limit append at a time; this ceiling stops that, and sits far enough above 16384 that it never blocks an append to a body the CLI wrote long. The CLI has no such ceiling.
- The same holds for every other constraint a schema advertises — `required`, `minLength`, `maxItems`, `minimum` / `maximum`, `enum`, and `additionalProperties: false` at every depth: the server checks each call's arguments against the schema it advertises, and a violation is a validation error (code 2, §5.5) naming the argument and the bound, with nothing written. Examples: `ask new: links exceeds 32 items (got 33)`, `ask new: verifier.timeout_seconds must be between 1 and 3600 (got 0)`, `ask show: unknown argument "note" (accepted: id)`. An optional argument is omitted to leave it unset; an explicit empty string where the schema says `minLength: 1` or lists an `enum` is a violation, not "unset". An explicit JSON `null` is read as the argument being absent, at every depth: an optional argument sent as `null` takes its default or is left unchanged exactly as if the key were missing (`"body": null` on `ask_update` does not clear the body), and a `required` argument sent as `null` is missing (code 2). `title` is the one argument measured after trimming surrounding whitespace.
- Response framing: every tool returns `{ "content": [{ "type": "text", "text": "<json>" }] }` on success, where `<json>` is the same payload the CLI emits with `--json`. The JSON-encoded payload is in the `text` field (single-string convention; agents JSON-parse the `text`).
- `isError` is **omitted** from a successful result — the key is absent, not `false`. Per MCP an absent `isError` means false, so a client must read it as "false unless present and true" rather than require the key. It is present, as `true`, only on the §5.5 error envelope. Where this document says a result is "`isError: false`" it means that reading: no `isError` key on the wire.
- `_meta`, the extension object MCP reserves on every result, is **omitted** from every result but one: an `ask_list` that skipped unreadable item files sets `_meta["ask/skipped"]` (§7.2). No other tool and no error envelope emits `_meta`, and `ask/skipped` is the only key ask defines in it. It adds to the result and replaces nothing: `content` is the same with or without it.

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

Return: the created Item object (§1.1) as `content[0]`. For each act-shaped `blocks` ref (§1.6), one further `type: "text"` part follows, carrying exactly the stderr line `ask new` writes: `ask new: act ready ignores --blocks until the act issue carries the dep; run: act dep add <act-id> --external <new-ask-id>`. With no act-shaped ref the result has the single part it always had. No `_meta` is set.

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

Return: an array of Item objects (§1.4). Item files that cannot be read are skipped and reported twice: as extra `content` entries after the array, and as data in the result's `_meta["ask/skipped"]`; see §7.2 for the shape.

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

### 5.4.1 `ask_update`

The MCP twin of `ask update` (§1.8.1): corrects `title`, `body`, and/or `verifier` in place, makes no state transition, allowed in any status.

```json
{
  "type": "object",
  "required": ["id"],
  "additionalProperties": false,
  "properties": {
    "id":          { "type": "string", "minLength": 1 },
    "title":       { "type": "string", "minLength": 1, "maxLength": 200 },
    "body":        { "type": "string", "maxLength": 16384 },
    "body_append": { "type": "string", "maxLength": 16384 },
    "verifier":       { "type": "string", "minLength": 1, "maxLength": 4096 },
    "verifier_clear": { "type": "boolean" }
  }
}
```

- `title` replaces the title (same validation and trimming as `--title`).
- `body` replaces the body; an explicit empty string clears it, an omitted field leaves it alone.
- `body_append` appends after a blank line, as `--body-append`. The cap applies to the piece appended, not to the body that results: a `body_append` of up to 16384 characters is accepted whatever the length of the stored body, including a body the CLI already grew past 16384 (§1.1). An over-length argument is refused with `ask update: body_append exceeds 16384 characters (got <n>)`. The body that results has its own, much higher ceiling of 1048576 characters: an append that would take it past that is a validation error (code 2) and writes nothing, with `ask update: body_append would grow body past 1048576 characters (body is <n> now, would be <m>)`.
- `verifier` replaces the verifier's command, as `--verifier`: a string (the command), not `ask_new`'s verifier object, since only the command is editable here. An existing verifier keeps its `type` and `timeout_seconds`; an item with none gains a shell verifier with `timeout_seconds: 60`. An empty or whitespace-only value is refused with `ask update: verifier must not be empty (use verifier_clear to remove the verifier)`.
- `verifier_clear: true` removes the verifier, as `--verifier-clear`. `verifier_clear: false` names no change.
- `body` and `body_append` together are a validation error (code 2), as are `verifier` and `verifier_clear: true` together, and a call naming no field at all.
- `--body-file` / `--body-append-file` have no twin: an MCP caller already holds the text.

Return: the updated Item object. When the item already reads as requested, no write happens and the call returns the §5.5 no-op envelope, with `content[1].text == "ask update: <id> already reads as requested"`.

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

- `code` is the exit-code taxonomy value (§2), and is always one of `2`, `3`, `4`, `5`, `7` — the codes a tool call can fail with. It is never `6`: an idempotent no-op is a success (next bullet) and carries no error envelope, so `6` exists only as a CLI exit status.
- `message` is the same human-readable string the CLI would have printed to stderr.
- Idempotent no-ops (code 6) are returned as `isError: false` with the item payload **and** an additional `content` text element containing the warning, mirroring the CLI's stdout-success-plus-stderr-warning pattern. (Justification: an MCP-side warning that arrives as `isError: true` is the wrong affordance — the call succeeded.) Concretely:
  - The `content` array has exactly two entries, both `type: "text"`.
  - `content[0].text` is the JSON-encoded post-call Item (identical to the active-transition success body), so clients parsing `content[0]` always get an Item regardless of whether the call was a real transition or a no-op.
  - `content[1].text` is the verbatim warning string `ask <verb>: already <status>` (for `ask_update`: `ask update: <id> already reads as requested`), matching the CLI's stderr line and serving as the structured no-op signal an orchestrator/verifier can match on without diffing item state.
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

- Exit code 5 — except the missing-`config.json`-with-items-present case, which is exit 7 (§7.1).
- Stderr message names the file and the failure reason, e.g.:
  - `ask: cannot read .ask/config.json: no such file or directory (run 'ask init')`
  - `ask: .ask/config.json is invalid JSON: unexpected EOF`
  - `ask: .ask/config.json missing required field "project_id"`
  - `ask: .ask/config.json has invalid project_id (expected ULID)`
- No file is written. No item is touched. No auto-recovery is attempted in v1.

### 7.1 Missing `config.json` with items present (stranded store)

The "file does not exist" condition above splits in two, and the split is load-bearing:

- `.ask/items/` holds no item files → the directory is not a store. Exit **5**, message `ask not initialized at <dir> (run 'ask init')`.
- `.ask/items/` holds one or more item files → the store is **stranded**: those asks are real, still want a human, and are unreachable until the config is restored. Exit **7**, with a message naming the directory and the item count, e.g. `.ask/config.json is missing but .ask/items/ holds 4 item file(s): this store is stranded, not empty (run 'ask init' here to restore access; items are preserved)`.

Reporting both cases identically is how a full inbox gets counted as zero by anything sweeping for `.ask` directories. Consumers branch on the exit code, not on the English.

### 7.2 Reads never create a store

Opening a store for a read (any verb other than `ask init`) is **non-mutating**: no `.ask/` and no `.ask/items/` is created, whether the open succeeds or fails. Only `ask init` creates `.ask/`, and only the first item write creates `.ask/items/`. The same holds for everything else a store can hold. `.ask/.lock` is created by the first write that takes the lock (§8.2), `.ask/subs/` by the first `ask subscribe` that stores something (§14.1), and `.ask/fires/` by the first fire (§14.5); `list`, `show`, `fires` and the MCP tools of the first two create none of them, and a `subscribe` that is refused creates no `subs/`. A read that fails must leave the directory exactly as it found it — otherwise a single `ask list` in the wrong directory enrols it permanently as a store-shaped husk that later sweeps cannot distinguish from a real one.

Item files (`.ask/items/ask-XXXX.json`) follow the same policy individually: a corrupt item file makes that one id unreadable (`ask show ask-XXXX` → exit 5, `ask list` skips it and emits a stderr warning per skipped id but continues with the rest, exit 0 if at least one item was readable, exit 5 only if every requested item was unreadable). The skip-and-warn behavior is the only deviation from "no auto-recovery"; it is necessary so that one bad file does not lock the entire inbox.

The details of that rule, for `ask list` and `ask_list` alike:

- **Unreadable** means the file cannot be loaded as a v1 item: it is not valid JSON, it carries a `schema_version` other than `"1"` (§1.1.1), its `id` field is not the file's own name stem, or the file itself cannot be read. Listing never rewrites, moves or deletes such a file.
- **The `id` field must equal the filename stem** (`ask-3c89.json` holds `"id": "ask-3c89"`). A mismatch makes the item unreadable under its filename id, with a reason naming both ids: the file name, not the file's contents, says which item a file is, so `harvest` (which refuses the whole run, §1.1.1) can neither write to nor `--clean` a path taken from an item's contents. Independently, the store refuses to build a path from any id that does not match the §1.1 pattern `^ask-[0-9a-f]{4}$` exactly (lowercase only, so two ids can never name one file on a case-insensitive filesystem): such an id cannot be loaded, saved or cleaned, a stray file so named in `items/` is an unreadable item, and `harvest` refuses the whole run naming every one. A stray on the target side whose name differs from a source item's file name only in case is refused too (§1.1.1).
- **The warning** is one stderr line per skipped id, in id order: `ask list: skipped <id>: <reason>`, where `<reason>` is the same reason `ask show <id>` reports for that item. A stray file, one whose name is not a valid id, is the one exception: no `ask show` can address it, `<id>` is its name stem, and the reason names the file itself, `ask list: skipped ASK-AAAB: invalid ask id: stray file in .ask/items/ not named for an id: ASK-AAAB.json`. Warnings go to stderr only; stdout is exactly the list, so `ask list --json` stays a valid JSON array (§1.4) holding the readable items.
- **Readability is judged before the filters run.** An unreadable file's status, urgency, `blocks` and `recipient` are unknown, so it cannot be matched against `--status` and the rest; it is always reported as skipped, whatever filters were given. The exit is `0` whenever at least one item file was readable — even if the filters then exclude every readable item, which lists nothing (`[]` under `--json`).
- **Exit 5** is reserved for a store where item files exist and none of them is readable. The per-id warnings are followed by `ask list: no readable items (<n> unreadable)` and stdout is empty — not `[]`, which would read as an empty inbox.
- **An empty store** (no item files at all) is not this case: it lists as empty and exits `0` with no warning.
- **Detecting a partial list without reading stderr: `ask list --strict`.** By default a partial list and a complete one differ only on stderr, so a consumer that reads stdout and the exit code alone cannot tell them apart: `ask list --status=open --urgency=blocker --json` prints `[]` and exits `0` both when there are no blockers and when the only blocker is in a file that was skipped. `--strict` is the signal for that consumer. With it, stdout is byte-identical to the run without it (the readable items; `[]` under `--json` if the filters match none of them) and the per-id warnings are the same, but if any item file was skipped the exit is `5` — the code every other verb returns for an unreadable item (§2) — and one more stderr line follows the list: `ask list: --strict: <n> unreadable item file(s) skipped; the list is incomplete`. With nothing skipped `--strict` changes nothing, and the no-readable-item case above is exit `5` with or without it. Exit `0` under `--strict` therefore means the list is complete. Like the skip itself, this is judged before the filters run: a skipped file fails a `--strict` list whatever filters were given, because nobody can say whether it would have matched.
  - *Why opt-in, and why the exit code.* The default cannot change: existing callers parse the `--json` array and treat exit `0` as success, so a `skipped` field (which would turn the array into an object) or a non-zero default exit would break them, and would undo the point of skip-and-warn — one bad file must not take down the list for everyone. A caller for whom "empty" has to mean "really none" (a blocker poll before claiming work, a sweep that counts open asks) asks for `--strict`; everyone else keeps a list that survives a bad file. No new exit code was minted because the condition is not new: it is an item file that could not be read, which is already `5`.
  - *What exit `5` from `ask list --strict` does not tell you.* It is also the code for a store that cannot be opened at all. The two are told apart by stdout: a partial list still prints the list (a JSON array under `--json`), a store that could not be listed prints nothing.
- **Other verbs are unaffected by a neighbour's bad file.** Id and prefix resolution reads file names, not file contents, so `show`, `update`, `resolve`, `reopen`, `close` and `new` keep working on every readable id; only a verb addressed at the unreadable item itself fails, with exit 5 and the file left byte-identical.
- **MCP shape (`ask_list`).** Skips are a success with warnings, in the §5.5 style: `isError: false`, `content[0].text` is the JSON array of readable items (so a client parsing `content[0]` always gets an array), and each skipped id adds one further `type: "text"` entry carrying the same line the CLI writes to stderr, in id order. A store with nothing skipped returns the single entry it always has. So `ask_list` needs no `strict` argument and has none: the result itself says whether the list is partial, and a client that reads the whole result cannot miss it. The structured signal is the result's `_meta` object (§5.1), which carries the skips as data under the key `ask/skipped`: `"_meta": {"ask/skipped": [{"id": "ask-b4ba", "reason": "<reason>"}]}`, one entry per skipped id in id order, where `id` is the file's name stem and `reason` is exactly the `<reason>` of that id's prose line. `_meta` is present exactly when something was skipped: the list is partial if and only if the result has `_meta["ask/skipped"]`, and a store with nothing skipped returns a result with no `_meta` key at all, not an empty array. A consumer should read that key rather than parse the prose entries or count them (`content` also holds more than one entry exactly when something was skipped, and the prose entries stay for clients and people that read them). Like the skip itself, the key is judged before the filters run, so it is present when the filters leave `content[0]` as `[]`. When no item file is readable the call returns the §5.5 error envelope with `code: 5`, and since there is no stderr the one `message` names every skipped id: `ask list: no readable items (<n> unreadable): skipped <id>: <reason>; skipped <id>: <reason>`. That error result carries no `_meta`: `isError: true` with `code: 5` is already its structured signal.

---

## 8. Atomic writes and the store lock

### 8.1 Atomic rename

Every write to `.ask/config.json` or any `.ask/items/ask-XXXX.json` uses the same pattern:

1. Marshal JSON.
2. Write to `<target>.<random>.tmp` in the same directory (same-volume guarantee for rename atomicity). The random part is 16 hex characters, fresh for every write, and the file is created exclusively, so two writers of one target never share a temp file.
3. `os.Rename(<that temp file>, <target>)`.

A reader therefore sees either the whole old file or the whole new one, never a partial write. The spec does **not** wrap rename with any retry or fallback.

Edge cases:

- A write that crashes between steps 2 and 3 leaves its temp file behind. It still ends in `.tmp`, so every reader of `.ask/items/` skips it; nothing removes it. No staleness handling is needed, because no later write reuses the name.
- The temp files are written under the same directory as the target so `os.Rename` never crosses volumes.

### 8.2 The store lock

Atomic rename keeps one write whole; it does not stop two writers from each loading an item, changing their copy, and saving it, the second save discarding the first change. One advisory lock per store does:

- The lock is `flock(LOCK_EX)` on `.ask/.lock`. The file is created by the first write that needs it and is never removed; its content is never read.
- `resolve`, `reopen`, `close` and `update`, on the CLI, and `ask_resolve`, `ask_reopen`, `ask_close` and `ask_update` over MCP, hold the lock across the whole sequence: resolve the id prefix, load the item, apply the change, save. Both surfaces call one function for this, so they cannot drift apart.
- `ask subscribe` and `ask unsubscribe` hold the lock the same way: resolve the id prefix, load the item and its subs file, change the subs file, save. A subscription and a status change on one item therefore never interleave: the change either sees the subscription and fires it, or does not see it and the subscriber is told the item's status as it was (§14.2).
- A status change appends the `started` line of each fire it causes (§14.5) while it still holds the lock, and starts the runners after releasing it. A runner takes no lock at any point: its one write is a single `write(2)` appending the fire's result line to a file opened in append mode, so lines from any number of runners and writers never interleave.
- `ask harvest` holds the target's and the source's locks for the whole operation, taken in path order so two harvests in opposite directions cannot each hold one.
- The lock is taken by trying a non-blocking `flock` every 25 ms for at most 10 s. On timeout the verb exits `5` (MCP code `5`) with `ask <verb>: store is locked by another ask process (waited 10s); nothing was written`. Every locked section is a few milliseconds, so the timeout only fires when another process is stuck while holding the lock. A process that dies releases its lock.
- Reads (`list`, `show`, `fires`, and the MCP tools of the first two) take no lock, never wait on a writer, and never create `.ask/.lock`.
- `ask new` and `ask init` take no lock. `ask new` writes a file no other writer has loaded; its only race is the id collision described in §4.2.
- The command's time is read once, before the lock is taken, and that one value is what a transition stamps (`resolved_at`, `closed_at`, `verified_at`).

The lock is advisory and unix-only (`flock`); ask builds for darwin and linux. It serializes ask processes on one host. Two hosts writing one store over a network filesystem are outside what it promises.

---

## 9. Verifier env inheritance

When an agent (not `ask` itself, in v1) runs a verifier:

- **Working directory:** the directory containing `.ask/`. The agent is responsible for `cd`-ing or equivalent.
- **Environment:** the full calling process environment, including `PATH`. No allowlist, no scrubbing.
- **Timeout:** `verifier.timeout_seconds` is advisory; ask does not enforce it. The agent honors it (typically via `context.WithTimeout` or shell `timeout`).
- **Capture:** stdout and stderr both captured, concatenated in chronological order if possible (else stdout then stderr), then trimmed to ≤65536 chars for storage in `verification_output` (§1.1).

Rationale for no allowlist: verifiers are user-authored commands that typically rely on the user's shell environment (PATH, NVM, ASDF, virtualenv activations, AWS_PROFILE, etc.). Scrubbing them would make most useful verifiers fail. Since `ask` does not execute the verifier itself in v1, this is also a description of what agents should do, not what `ask` enforces.

A callback handler (§14.3) is the opposite case, and gets the opposite rule. ask runs a handler itself, and runs it from whatever process happened to change the item: a person's shell, an agent's session, a long-lived MCP server. That process's environment is not the handler author's and can hold credentials the handler has no business seeing, so a handler's environment is built from a fixed list and inherits nothing else. A verifier is run by the agent that chose to run it, in an environment that agent already has; ask never runs it, so ask has nothing to build.

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

## 14. Callbacks: subscriptions, handlers and fires

A subscription says "when this item is resolved, closed or reopened, run this handler". It exists so that a party other than the one that filed an item can be told when the item changes, without polling. ask stores subscriptions beside the item, runs the handlers when the item's status changes, and keeps a record of every run. Nothing here changes the Item object (§1.1), `ask show --json`, or any MCP tool's arguments or results.

`ask help callbacks` carries the working summary; `docs/callbacks.md` is the guide to writing a handler.

### 14.1 The subs file

An item's subscriptions live in `.ask/subs/<item-id>.json`. An item with no subscriptions has no file: the file is written by the first subscription stored and removed with the last one removed. `.ask/subs/` is created by the first write.

```json
{
  "schema_version": "1",
  "item": "ask-3c89",
  "subscriptions": [
    {
      "id":       "6d69ca27",
      "events":   ["resolve", "close", "reopen"],
      "handler":  "record",
      "args":     { "thread": "abc" },
      "added_at": "2026-05-15T10:30:00Z"
    }
  ]
}
```

| Field | Constraints |
|---|---|
| `schema_version` | the string `"1"`. A file carrying another value is unreadable, on the footing of an item file in another version (§1.1.1). |
| `item` | the id the file is named for. A file naming another item is unreadable. |
| `subscriptions[].id` | 8 lowercase hex characters, random, unique within the file |
| `subscriptions[].events` | a non-empty subset of `resolve`, `close`, `reopen`, always in that order. `any` on the command line is stored as all three. |
| `subscriptions[].handler` | a handler name (§14.3) |
| `subscriptions[].args` | object of string to string, `{}` when none. At most 16 entries; each key matches `^[a-z][a-z0-9_]{0,31}$`; each value is at most 1024 bytes and holds no NUL. Arguments are data: ask hands them to the handler and never interprets them. |
| `subscriptions[].added_at` | RFC 3339 UTC, when the subscription was stored |

An item holds at most 32 subscriptions. The file is written by atomic rename (§8.1) under the store lock (§8.2). An unreadable subs file (corrupt, another version, another item's, or a stray in the way, §1.1.1) makes every verb that reads or writes the item's subscriptions exit 5; a status change on the item still goes through and fires nothing.

### 14.2 `ask subscribe` and `ask unsubscribe`

Both are CLI-only.

```
ask subscribe <id> --on <event>[,<event>] --handler <name> [--arg key=value ...] [--json]
```

`--on` takes `resolve`, `close`, `reopen`, or `any`, comma-separated. `--arg` is repeatable; a flag without `=`, or a key given twice, is refused. `subscribe` stores the subscription and reports the item's status as it was under the lock. It never runs the handler.

- Without `--json`: `ask-3c89: subscribed 6d69ca27 (resolve,close,reopen -> record); status open` on stdout; `already subscribed` in place of `subscribed` when nothing was added.
- With `--json`: `{ "id": "ask-3c89", "status": "open", "subscription": { ...the stored record... } }`.

Exit codes, checked in this order:

| When | Exit | Stored? |
|---|---|---|
| the item is `closed` (a closed item never changes again); the handler name breaks the name rule; no installed handler has that name (stderr lists the installed ones); an argument breaks its rule; the item already holds 32 subscriptions | `2` | no |
| the item is already `resolved` and the subscription names `resolve` | `8` | yes: it fires if the item is reopened and resolved again. A stderr line says so. |
| an identical subscription (same handler, same arguments, same set of events) is already stored | `6` | no; the existing one is reported |
| otherwise | `0` | yes |

`3`, `4` and `5` are as for every verb that addresses an item. Because `subscribe` holds the store lock while it reads the status, there is no window in which the item is resolved between the check and the store: the caller either gets `0` and the handler fires on the resolve, or gets `8` and knows to read the item now.

```
ask unsubscribe <id> <subscription-id> [--json]
```

The subscription id is the 8-hex id `ask show` lists, or any prefix of it (1 to 8 lowercase hex characters) that matches one subscription of that item. Prints `ask-3c89: unsubscribed 6d69ca27`, or with `--json` `{ "id": "...", "unsubscribed": { ...the removed record... } }`. Exit `3` when the item or the subscription is not found, `4` when either prefix is ambiguous. A fire already started for a subscription that is then removed is recorded as `not_run` (§14.5).

### 14.3 Handlers

A handler is an executable file at `~/.config/ask/callbacks/<name>`, in the home directory of the user ask runs as, on the host where the store lives. Handlers are installed by hand; ask never writes to that directory.

- **Name:** `^[a-z0-9][a-z0-9_-]{0,63}$`. Lowercase only, no separator, no leading dash or dot, so a name is always one file directly under the callbacks directory and two names never address one file on a case-insensitive filesystem.
- **At subscribe time** the name must resolve, following symlinks, to a regular file with an execute bit; otherwise exit 2 (§14.2). This is a check for typos, not a guarantee: the handler is looked up again at every fire.
- **At fire time** the handler is started by its path under the callbacks directory, so `$0` is the name it was subscribed by even when that path is a symlink.

What a handler is run with:

| | |
|---|---|
| Working directory | the project root: the directory that holds `.ask/` |
| Standard input | one JSON object, no trailing newline: `{ "item": <Item, §1.1>, "event": "resolve", "from": "open", "to": "resolved", "at": "<transition time>", "args": { ... } }`. `item` is the item as the store holds it when the handler starts, which is after the change that fired it. |
| Standard output and error | one pipe; the last 4096 bytes are kept in the fire record |
| Time | 60 seconds |

The environment is **built, not inherited**. It holds exactly these variables and nothing else from the process that changed the item:

| Variable | Value |
|---|---|
| `PATH` | `/usr/bin:/bin:<the directory holding the ask binary>:$HOME/.local/bin` |
| `HOME` | the user's home directory |
| `LANG` | the caller's `LANG`, or `en_US.UTF-8` when it has none |
| `ASK_ID` | the item id |
| `ASK_EVENT` | `resolve`, `close` or `reopen` |
| `ASK_SUB_ID`, `ASK_FIRE_ID` | the subscription's and the fire's 8-hex ids |
| `ASK_FROM_STATUS`, `ASK_TO_STATUS` | the item's status before and after the change |
| `ASK_AT` | the transition time, RFC 3339 UTC: the instant the item's own timestamp carries for this change |
| `ASK_STORE` | the project root |
| `ASK_ARG_<KEY>` | one per argument, the key upper-cased |

§9 says why a verifier's environment is inherited and a handler's is not.

The contract a handler author can rely on:

- **At most once.** A change fires each subscription that names it once. A handler is never retried: not after a non-zero exit, not after a timeout, not when it could not be started, not when the host went down while it ran. Nothing delivers a fire later.
- **It cannot touch the write.** The item was saved before any handler was started, and the command that changed it has not waited. Nothing a handler does, or fails to do, changes that command's exit code, its output, or its MCP result.
- **60 seconds.** At the timeout the handler's process group is sent SIGTERM, and SIGKILL 2 seconds later, so what the handler started goes with it. A handler that needs longer starts its own detached work and returns.
- **Output is stored and shown. Never print a secret.** The last 4096 bytes a handler writes to stdout and stderr go into the fire record (§14.5), a file in the store, and `ask fires` prints them to anyone who can read the store. `ask harvest` carries them to another store.
- **Its own settings live elsewhere.** Every file in the callbacks directory is a handler, so a handler keeps configuration and credentials out of it. The convention is `~/.config/ask/callback-config/<name>.env`, mode 0600, read by the handler itself. ask neither reads nor creates it.

### 14.4 Firing

A fire happens when a write changes an item's status, through the CLI or an MCP tool alike:

| Change | Event |
|---|---|
| `open` to `resolved` | `resolve` |
| anything to `closed` | `close` |
| `resolved` to `open` | `reopen` |

A change that leaves the status alone fires nothing: an idempotent no-op (exit 6), `ask update`, `ask new`, `ask subscribe`.

The sequence, for a status change on an item with subscriptions:

1. Under the store lock, after the item is saved, the changing process reads the item's subs file and, for each subscription whose `events` include this event, appends a `started` line to the item's fire record (§14.5). A subscription whose `started` line cannot be written is not fired: a fire that cannot be recorded does not run.
2. The lock is released and the command returns as it always did. Firing cannot fail the write or change what the command prints.
3. For each recorded fire the process starts one detached runner (ask re-executing itself, in a new session, with the project root as its working directory and its standard streams on `/dev/null`) and does not wait for it. The new session is deliberate: a supervisor that kills a finished job's process group, or a terminal that hangs up, would otherwise take the runner down before the handler ran.
4. The runner loads the subscription and the item from the store itself, checks that the subscription is still there and still names the event, runs the handler (§14.3), and appends the fire's `result` line. It takes no lock.

The runner is an internal verb of the binary, not part of the CLI surface. It and the process that started it can be different builds (a long-lived MCP server re-executes whatever file is at its own path, which an upgrade may have replaced), so the invocation carries a protocol version, and a runner handed one it does not know records `spawn_failed` rather than guess.

### 14.5 The fire record

`.ask/fires/<item-id>.jsonl` is append-only and holds two JSON lines per fire, paired by their `fire` id. The file and `.ask/fires/` are created by the first fire of that item, never by a read; the file is mode 0600, because a result line holds handler output.

```json
{"v":1,"fire":"cafbd927","sub":"6d69ca27","handler":"record","event":"resolve","from":"open","to":"resolved","at":"2026-05-15T10:30:00.123456Z","phase":"started","ts":"2026-05-15T10:30:00.124Z"}
{"v":1,"fire":"cafbd927","sub":"6d69ca27","phase":"result","state":"ok","exit_code":0,"detail":null,"duration_ms":212,"ts":"2026-05-15T10:30:00.340Z","output":"recorded\n"}
```

| Key | Line | Meaning |
|---|---|---|
| `v` | both | record version, the integer `1` |
| `fire` | both | 8 lowercase hex characters, new for every fire |
| `sub` | both | the subscription's id |
| `handler`, `event`, `from`, `to` | started | what is being run, for which change |
| `at` | started | the transition time, the same instant as the item's `resolved_at` or `closed_at` for that change |
| `phase` | both | `started` or `result` |
| `ts` | both | when the line was written, RFC 3339 UTC |
| `state` | result | see below |
| `exit_code` | result | the handler's exit code, or `null` when it has none |
| `detail` | result | a short reason, or `null` |
| `duration_ms` | result | from the start of the run to the result line |
| `output` | result | the last 4096 bytes of the handler's stdout and stderr together; `""` when it never ran |

Result states:

| `state` | Meaning | `exit_code` | `detail` |
|---|---|---|---|
| `ok` | the handler exited 0 | `0` | `null` |
| `failed` | the handler exited non-zero, or died on a signal | the code, or `null` for a signal | `null`, or the signal |
| `not_run` | the handler was never started | `null` | `no handler named <name>`, `not runnable` (the path is not an executable regular file), or `subscription removed before it ran` |
| `spawn_failed` | the runner or the handler could not be started, or the runner could not use what it loaded (an unreadable subs file or item, a stored argument that breaks its rule) | `null` | the operating system's error, the reason, or `runner protocol mismatch` |
| `timeout` | the handler was still running at 60 s and was killed | `null` | `null` |

A `started` line with no `result` line is a fire whose handler is still running, or whose runner died before it could write one. Readers report it as state `started`; it is never written to the file.

Each line is one `write(2)` on a descriptor opened in append mode, so lines from the process that changed the item and from any number of runners never interleave. The record is a file on disk and readers do not trust it: a symlink where the file or `.ask/fires/` should be is refused, not followed; the file is found by its exact name among the directory's entries, so a file named for another spelling of the id is not the item's; a line that is not a record line in version 1 (not JSON, another `v`, an id not in the shape above, an unknown phase, or longer than 64 KiB) is left out, as are a `started` line repeating a fire id, a `result` line pairing with no `started` line, and a second `result` line for one fire.

### 14.6 Reading fires: `ask fires` and `ask show`

```
ask fires [<id-or-prefix>] [--json]
```

CLI-only. With an id, that item's fires, oldest first; without, every fire in the store, oldest first by when each started. It takes no lock and creates nothing. Exit `0` including when there are no fires; `2` for more than one id; `3` / `4` / `5` as for every verb that addresses an item.

- With `--json`: an array, `[]` when there are no fires, of

  ```json
  {
    "item": "ask-3c89", "fire": "cafbd927", "sub": "6d69ca27", "handler": "record",
    "event": "resolve", "from": "open", "to": "resolved",
    "at": "2026-05-15T10:30:00.123456Z",
    "started_at": "2026-05-15T10:30:00.124Z", "finished_at": "2026-05-15T10:30:00.34Z",
    "state": "ok", "exit_code": 0, "detail": null, "duration_ms": 212, "output": "recorded\n"
  }
  ```

  in that key order. `started_at` and `finished_at` are the `ts` of the two lines. For a fire with no result line `state` is `"started"`, `finished_at`, `exit_code`, `detail` and `duration_ms` are `null`, and `output` is `""`. `output` is exactly what was stored.
- Without `--json`: one block per fire, blocks separated by a blank line:

  ```
  fire cafbd927  item ask-3c89  subscription 6d69ca27  handler record
    2026-05-15T10:30:00Z  resolve  open -> resolved
    ok, exit 0 in 0.2 s
    output:
      recorded
  ```

  The third line is the state in the words listed below. The `output:` block is left out when the handler printed nothing. If lines of the record were left out (§14.5), stderr says how many, per item.

`ask show <id>` in its text form lists an item's subscriptions under `Subscriptions:`, one line each (`- <id> <handler> on <events> [key="value" ...] added <time>`), and under each subscription its latest 3 fires, oldest first, one line each:

```
<time>  <event>   ok, exit 0 in 0.4 s
<time>  <event>   failed, exit 22 in 7.2 s
<time>  <event>   failed, <signal> in 7.2 s
<time>  <event>   not run: no handler named <name> on this machine
<time>  <event>   not run: the handler <name> is not an executable file on this machine
<time>  <event>   not run: subscription removed before it ran
<time>  <event>   not run: could not be started: <detail>
<time>  <event>   killed at the 60 s timeout
<time>  <event>   started, no result after <age> (it is running)
<time>  <event>   started, no result after <age> (it died; it is not retried)
```

A fire with no result line reads "it is running" until 90 seconds after it started, which is past the timeout and the kill, and "it died" from then on. When a subscription has nothing of the kind to show, exactly one line says why:

```
not fired yet: no <events> since this was added
missed: <event> at <time> by a build that does not fire; not retried
```

`missed` is §14.8. A `missed` line is also listed, in time order with the fires, when a subscription has fires as well. If the fire record cannot be read, each subscription gets `fire record unreadable: <reason>` in place of these lines and `show` still exits 0.

`<time>` is the transition time, RFC 3339 UTC to the second. The durations and `<age>` (`12 s`, `4 min`, `3 h`, `9 d`) are the only relative figures. `ask show` never prints a handler's output; that is `ask fires`. Everything either verb prints as text from a subs file or a fire record has its non-printable characters replaced by spaces (control characters and escape sequences, and the invisible ones that reorder or hide text), so a stored value cannot forge a line or drive the terminal it is read on. `--json` carries values as stored.

### 14.7 `ask harvest`

`ask harvest` copies, for each source item: the item, its subs file if it has subscriptions, and its fire record if it has one. Of the record it carries the record lines (§14.5) as they were written and no other line. With `--clean` all three source files are removed after every copy has succeeded. Without the record, a harvested item whose handler ran would meet the `missed` rule in the target store.

The all-or-nothing rule of §1.1.1 covers the two new files: a source subs file or fire record that cannot be read, and a target file in `subs/` or `fires/` whose name differs from a source file's only in case, each refuse the whole harvest before anything is written.

A fire whose handler may still be running refuses it too. The runner takes no lock (§14.4), so a harvest at that moment would carry the fire's `started` line without its result, and the runner would then write the `result` line into the source store (with `--clean`, re-creating the source record holding that line alone): in the target the fire would read as died for a handler that finished. So when any source item's fire record holds a fire with no result line whose `started` line was written less than 90 seconds ago (the running window of §14.6), harvest exits `5`, writes nothing to either store, and names each such item, fire and handler; the caller retries once the handler has finished, at most 90 seconds after it started. A `started` line older than that is a dead runner, whose result will never come, and does not refuse. Harvest refuses rather than waits so that it never holds both store locks for the length of a handler.

Harvest cannot tell whether a handler really ran. A carried fire record is the source store's account, on the same footing as its items and subscriptions. A harvested subscription names a handler on the host of the target store; if none is installed under that name there, its fires are recorded as `not_run`.

### 14.8 Builds without callbacks

A build of ask from before this section leaves subs files on disk and fires nothing, and a long-lived process keeps being the build it was started as: an `ask mcp` server started before an upgrade resolves and closes items without firing until it is restarted. Nothing is delivered afterwards.

The miss is on disk, and `ask show` states it. For each subscription: when the item's `resolved_at` is later than the subscription's `added_at`, the subscription names `resolve`, and the fire record holds no fire of that subscription for `resolve` at that time, `show` prints `missed: resolve at <time> by a build that does not fire; not retried`. The same holds for `closed_at` and `close`. A missed reopen cannot be detected, because a reopen stamps no time on the item (it clears `resolved_at`).

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
| §Concurrent safety: atomic rename via `<x>.tmp` | §8.1 — `os.Rename` of a uniquely named temp file; §8.2 adds the store lock |
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
| (new) Callbacks: subscriptions, handlers, firing, the fire record | §14 |

**v1: deferred items** (intentionally not pinned by this spec; tracked for post-v1):

- Concurrent-resume safety (two agents picking up the same `resolved` item and both acting on it). The store lock (§8.2) means only one of two concurrent transitions wins and the other sees exit 6, but ask does not hand out claims on a resolved item; revisit if dogfooding surfaces real loss.
- Concurrent `ask new` from two processes colliding on id. Documented in §4.2.
- Verifier execution by `ask` itself (`ask verify <id>` verb). Not in v1 surface; would be additive. ask executes one kind of thing, a subscribed callback handler (§14), and that does not change this: a handler is told that an item changed and decides nothing about it.
- Retrying a handler, delivering a fire that a build without callbacks missed, detecting a missed reopen, and firing on anything but a status change (a new item, an `update`). §14.4 and §14.8 say what happens instead.
- Aggregated cross-project inbox, TUI, web UI, custom item types. Listed in brief §Out of scope; spec adds nothing. Push notification by ask itself stays out of scope too: ask sends nothing anywhere. A handler (§14) is how a party that wants to be told arranges it, with its own code and its own credentials.

---

## 12. Forward-compatibility constraints

To keep post-v1 changes additive rather than breaking:

- **Item schema:** new fields must default to `null` or `[]` and must not change the meaning of existing fields. v1 parsers should ignore unknown fields on read (`json.Decoder` without `DisallowUnknownFields`). The `schema_version` field (§1.1.1) is the migration hook for any change that *can't* be expressed as a pure additive field — bump it and treat the old value as legacy on the read path.
- **`config.json`:** same. New fields default to absent-tolerant values.
- **Exit codes:** the taxonomy in §2 is closed; new error classes get new codes (`8`, `9`, ...) rather than reusing existing ones with new semantics. (`7` was added this way for the stranded-store case, §7.1, and `8` for a subscription stored after the event it names, §14.2.)
- **MCP tools:** new tools are additive. Existing tools' `inputSchema` may grow optional properties; required properties cannot change. Response shape changes are breaking and require a wire-version bump.
- **Subs files and fire records:** a subs file carries its own `schema_version` (§14.1) and every fire record line its own `v` (§14.5), independent of the Item's `schema_version`. A subs file in another version is unreadable, like an item in another version; a fire record line in another version is left out by readers. New keys in either are additive.
- **CLI flags:** new flags must be optional. Renaming an existing flag is breaking and requires a major-version bump.

---

End of spec.
