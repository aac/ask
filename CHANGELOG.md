# Changelog

All notable changes to `ask` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- MCP `ask_list` reports skipped item files as data. When an unreadable item file was
  skipped, the result carries `"_meta": {"ask/skipped": [{"id", "reason"}]}`, one entry per
  skipped id, so a program can tell a partial list from a complete one without parsing the
  prose warning. Nothing else in the result changes: `content[0]` is still the array, the
  prose warning entries are still there, `isError` is still absent, and a list with nothing
  skipped has no `_meta` key. It is the MCP counterpart of `ask list --strict`.
- `ask list --strict` exits 5 when any item file was unreadable and skipped. Output is the
  same as without the flag (the readable items on stdout, one warning per skipped id on
  stderr); only the exit code changes, and only when something was skipped. It is how a
  caller that reads stdout and the exit code alone tells a complete list from a partial one:
  `ask list --status=open --urgency=blocker --json` prints `[]` and exits 0 both when there
  are no blockers and when the one blocker sits in a skipped file. The default is unchanged.
- `ask subscribe <id> --on <event>[,<event>] --handler <name> [--arg key=value ...]` and
  `ask unsubscribe <id> <subscription-id>` store and remove a subscription: "when this item
  is resolved, closed or reopened, run this handler". A handler is an executable at
  `~/.config/ask/callbacks/<name>` on the host where the store lives. Subscriptions live in
  `.ask/subs/<id>.json`; `ask show` lists them, `ask harvest` carries them with their item,
  and the Item schema, `ask show --json` and the MCP tools are unchanged. `subscribe` exits 8
  when the item is already resolved and the subscription names `resolve` (stored anyway),
  6 for an identical subscription, 2 for a closed item.
- A status change runs the handlers subscribed to it: `resolve` for open to resolved, `close`
  for anything to closed, `reopen` for resolved to open, through the CLI and the MCP tools
  alike. The command that changed the item does not wait: it records the fire and starts a
  detached runner, which runs the handler from the project root with a built environment
  (`PATH=/usr/bin:/bin:<ask's directory>:$HOME/.local/bin`, `HOME`, `LANG`, `ASK_ID`,
  `ASK_EVENT`, `ASK_SUB_ID`, `ASK_FIRE_ID`, `ASK_FROM_STATUS`, `ASK_TO_STATUS`, `ASK_AT`,
  `ASK_STORE`, and `ASK_ARG_<KEY>` per argument; nothing inherited) and one JSON object on
  stdin (`item`, `event`, `from`, `to`, `at`, `args`). A handler has 60 seconds, after which
  it and everything it started are killed. Each fire leaves two lines in
  `.ask/fires/<id>.jsonl`, a `started` line and a result line whose state is `ok`, `failed`,
  `not_run`, `spawn_failed` or `timeout`, with the exit code and the last 4096 bytes of
  output. A failing handler never fails or changes the write and is never retried; exit
  codes, stdout and the MCP envelopes of `resolve`, `close` and `reopen` are unchanged.
- `ask version` distinguishes builds. Without an `-ldflags` stamp it now prints
  `dev+<commit>[.dirty] <commit time>` from the build info Go embeds in every build from a
  checkout, instead of the bare `dev` — so a stale install is visible from the binary itself.
  Release builds still print their stamp; MCP `serverInfo.version` reports the same string.
- `ask new` warns (never refuses) when the title or body uses relative-date language —
  `tomorrow`, `next week`, `in 3 days`, a bare `Monday` — and names the absolute date it
  resolves to at filing time, e.g. `"Tomorrow" ... (Tomorrow = 2026-09-26)`. Asks are read
  long after they are filed; "Tomorrow: run the dogfood" once sat for 63 days.
- `ask update <id>` corrects an item's text in place: `--title`, `--body`,
  `--body-file <path|->`, `--body-append`, `--body-append-file <path|->`. Flag names
  mirror `act update` so the sibling tools stay learnable together. It makes no state
  transition — an open ask stays open while its wording is fixed — which removes the two
  bad workarounds for a rotted ask: editing `.ask/items/<id>.json` by hand, and
  resolve-and-refile (which closes something nobody has done). Requesting text the item
  already has is an idempotent no-op: exit 6, no write.
- Exit code **7**, "stranded store": `.ask/config.json` is absent but `.ask/items/` holds
  item files. Previously indistinguishable from an uninitialized directory (both exit 5,
  same message), so an inbox full of open asks could be swept up and counted as zero.
  Surfaced on the CLI and in the MCP error envelope; `ask init` in that directory is the
  repair and adopts the existing items.

### Changed
- `make release-local` no longer builds `windows/amd64`. The store lock is unix-only, and
  Windows was already outside the platforms ask ships for.
- The skill's filing rules now carry a **has-agent-arm check**: before filing, state why no
  agent in reach can take the action. "I can't do this from here" is not "no agent can do
  this" — another machine may hold the credential, the tool, or the network path — and a body
  containing a runnable command is the signature of an item that belongs in a tracker, not in
  a human's inbox.

### Fixed
- The store refuses any item id that is not exactly `ask-` plus four lowercase hex characters,
  the shape it generates. It used to accept any path-safe string, so on a case-insensitive
  filesystem (the macOS default) `ask harvest` from a store holding `ASK-AAAA.json` passed the
  collision check and silently overwrote the target's `ask-aaaa.json`. Harvest now refuses the
  whole run (exit 5, nothing copied, every bad id named), and `ask list` skips a stray
  non-conforming file in `items/` with the usual per-id warning.
- A stray file in `items/` whose name is not a valid id (`ASK-AAAB.json`) no longer gets in
  the way of real items. It used to count as a prefix match, so `ask show aaa` beside
  `ask-aaaa.json` exited 4 (ambiguous), as did `ask show aaaa` beside an `ASK-AAAA.json` on a
  case-sensitive filesystem. Every verb now resolves to the real item; a prefix that matches
  only the stray exits 5 with a message naming the file. `ask harvest` now names an id
  collision and unreadable source items in one refusal instead of the collision alone.
- `ask harvest` no longer overwrites a stray file in the target store. With a hand-placed
  `ASK-AAAB.json` in the target and `ask-aaab` in the source, on a case-insensitive
  filesystem (the macOS default) harvest exited 0, wrote the ask into the stray file and left
  it named `ASK-AAAB.json`, so `ask list` skipped it; with `--clean` the ask was also gone
  from the source. Harvest now exits 5 naming the file, copying and removing nothing,
  whenever a name in the target's `items/` differs from a source item's file name only in
  case. It compares names, so the refusal is the same on a case-sensitive filesystem.
- Two writers of one item can no longer overwrite each other. `resolve`, `reopen`, `close`
  and `update` (and the MCP `ask_resolve` / `ask_reopen` / `ask_close` / `ask_update` tools) loaded an
  item,
  changed it and saved it with nothing held in between, so of two simultaneous
  `update --body-append` calls one note was silently lost, and two simultaneous `resolve`
  calls could both report the transition as theirs. Writers now hold one advisory lock per
  store (`flock` on `.ask/.lock`) across the whole load, change, save, through a single
  write path shared by the CLI and the MCP server; `harvest` holds both stores' locks. A
  writer that cannot take the lock within 10 s exits 5 with
  `store is locked by another ask process (waited 10s); nothing was written`. Reads take no
  lock. Each atomic write also uses its own temp file name, where every writer of one item
  used to share `<item>.json.tmp`. `ask new` is unchanged: two processes filing at the same
  instant can still collide on an id.
- The MCP server now enforces the length limits its tool schemas advertise. `body`,
  `body_append`, `note`, `reason`, `filed_by`, `tracker_ref`, `verifier.command` and link
  `label` / `url` each carried a `maxLength` that nothing checked, so a caller that ignored
  the schema could file or grow an item of any size. An over-length value is now a
  validation error (code 2) naming the field and the limit, and nothing is written.
  Each limit applies to the argument a call sends, not to what is stored (the one exception
  is the 1048576-character ceiling on the body a `body_append` produces). The CLI is
  unchanged: it applies no cap to `body` or the other free-text fields, on purpose, because
  an ask refused at filing time is lost (spec 1.1 now says so instead of reading as a hard
  limit).
- The MCP server now enforces the rest of what its tool schemas advertise, by checking each
  call against the schema itself: `links` is capped at 32 entries, a link's `label` and
  `url` are required and non-empty, `verifier.timeout_seconds` must be 1..3600 when given,
  and an argument the schema does not declare is refused (every tool advertises
  `additionalProperties: false`), including inside `verifier` and a link. All of these were
  accepted and written before. Also now refused, as the schemas always said: an explicit
  empty string for `recipient`, `urgency` or `verifier.type` on `ask_new`, and for the
  `blocks` / `recipient` filters on `ask_list` — omit the argument instead. Each is a
  validation error (code 2) naming the argument and the bound, and nothing is written.
- `ask help mcp`'s example transcript no longer shows a fixed `0.1.0` as the server
  version; `serverInfo.version` is the build's version string.
- `docs/spec.md` and `ask help mcp` now match the MCP wire: a successful result omits
  `isError` (absent means false), and the error codes a tool call can return are 2, 3, 4,
  5 and 7 — never 6, since an idempotent no-op is a success.
- A failed read no longer mutates the filesystem. `OpenStore` created `.ask/items/` before
  checking for `config.json`, so `ask list` in a directory with no store created a
  store-shaped husk there and *then* errored. Those husks are indistinguishable from real
  stores to anything scanning for `.ask` directories, so one stray read permanently
  enrolled a repo in every future sweep. `.ask/` is now created only by `ask init`, and
  `.ask/items/` only by the first item write.
- `--body-file -` / `--body-append-file -` (and any future `-`-valued flag) now parse
  correctly when the flag precedes the id; the flag reorderer treated a bare `-` as the
  start of another flag and swallowed the id as the flag's value.
- A verifier filed without a timeout is stored with `timeout_seconds` 60, as the spec has
  always said; `ask new --verifier` and the MCP `ask_new` both stored 0. Items already on
  disk with 0 still load and are reported as 60 by `ask show`, `ask list` and the MCP tools.
- The title limit is 200 characters, not 200 bytes. `ask new`, `ask update --title` and the
  MCP `ask_new` / `ask_update` tools counted bytes, so a 200-character title in a non-ASCII
  script was refused with `title must be 1..200 characters`. All four now count Unicode code
  points, through the one validator in `internal/core`; `ask new` no longer carries its own
  copy of the title rules.
- An MCP `body_append` to an item whose body is already past 16384 characters now succeeds.
  The CLI files bodies of any length, and `ask_update` refused every append to such an item
  with `body_append would grow body past 16384 characters`, leaving it uneditable over MCP.
  The 16384 cap is on the `body_append` argument, not on the body that results. The result
  has its own ceiling of 1048576 characters, so a client appending in a loop still cannot
  grow an item without limit: an append past it is a validation error (code 2) and writes
  nothing. The CLI has no ceiling.

## [0.2.1]

### Fixed
- MCP `serverInfo.version` now reads the stamped `internal/version.Binary` (the same
  source as `ask version`) instead of a hardcoded `0.1.0` literal, so it no longer
  drifts from the release version. verify-release check 7 gates this going forward.

## [0.2.0]

### Removed
- `ask install-skill` command and the in-binary skill `go:embed`. Skill delivery is now
  plugin-first: `/plugin install ask@ask` ships and auto-discovers the skill. The bare
  binary is CLI-only; a source install (`install.sh`) copies `skills/ask/` from the
  checkout, and non-plugin users can copy it from a repo clone.

## [0.1.0]

### Added

- Initial public release: single Go binary with an embedded skill and in-process MCP server.
- Self-contained plugin (per-repo marketplace) for Claude Code, Cowork / Claude Desktop, and Codex — the canonical install; the bundled binary runs with no separate setup.
- Non-plugin setup via `ask install-skill` (writes the skill to the host) and `ask mcp` (MCP server).
- Per-arch binaries (darwin / linux × amd64 / arm64) + uname launcher committed into the plugin and built by CI under the commit-to-main release model.
