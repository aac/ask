# ask distribution readiness

ask v1 has been solo-dogfooded on a single laptop where `~/.claude/skills/ask`
is a symlink into the source tree. The next stage is distribution to other
users. Symlink shortcuts don't travel; the binary becomes the only honest
distribution mechanism. This note catalogues the rough edges that surface the
moment a second user installs ask, with directions resolved by two rounds of
external design review.

**Status:** review-converged and settled. All open questions resolved
2026-05-18. Section ordering reflects shipping sequence — §0 gates
everything; §1–§3 are the v1.1 minimum for "a non-Andrew user can install
and use ask without help"; §4 onward is post-v1.1 hardening.

## §0. Binary distribution

**Problem.** Today the only documented install path is
`go install github.com/aac/ask/cmd/ask@latest`. That requires a Go toolchain, which most Claude Code users don't have. Every other rough edge in this note is moot if the binary can't reach a non-Go user — there *is* no user. R1 called Homebrew "downstream"; R2 corrected to "upstream of everything else here," and that's right.

**Direction.**
- **Primary (the floor):** GitHub Releases + GoReleaser cross-compiling darwin/linux ×
  arm64/amd64 in CI on tag push, plus a one-line install script
  (`curl -fsSL <url> | sh`) that picks the right asset and drops it on `$PATH`,
  then prints "now run `ask install-skill` and `claude mcp add --scope user ask -- ask mcp`."
- **Secondary (the upgrade):** Homebrew tap (`brew install aac/tap/ask`).
  GoReleaser publishes the formula automatically. Covers most macOS Claude
  Code users with a command they already trust.
- **Skip for v1.1:** macOS code-signing and notarization. Costs an Apple
  Developer account plus CI complexity that a pre-public solo tool doesn't
  earn. Document the `xattr -d com.apple.quarantine /usr/local/bin/ask`
  workaround in one README line; revisit when a real Gatekeeper complaint
  lands.

**Open questions.**
- Tap repo name and install-script host. `aac/tap` and `ask.tools/install.sh`
  are placeholders. Host on GitHub Pages or just the raw `main` branch of the
  release repo to start.
- Linux distribution beyond the install script (deb/rpm/AUR) is post-v1.1.

## §1. MCP registration

**Problem.** README mentions `ask mcp` (stdio MCP server) but says nothing
about how a user wires it into Claude Code's config. This has larger blast
radius than `install-skill` because hand-editing `~/.claude.json` is
error-prone, and the skill is useless if the agent can't reach the binary
through MCP. **Andrew has never actually exercised this path himself either**
— `claude mcp list` confirms ask is not in the local config — so this is also
a dogfood gap.

**Direction.** Don't build `ask mcp install`. Claude Code already ships
`claude mcp add` with the right shape. The canonical command is:

```
claude mcp add --scope user ask -- ask mcp
```

`--scope user` (vs the default `local`) makes ask available in every repo,
which matches the "global agent inbox primitive" framing. README documents
this single command. Skill text gains a one-line fallback for "if `ask help`
fails, the binary isn't on PATH — see README."

**Open questions.**
- None substantive. If `claude mcp add` ever changes shape, revisit. Until
  then, a wrapper subcommand is over-engineering.

## §2. README "Getting started" rewrite

**Problem.** The README's current Getting Started section is a Go-developer
doc (`go install`, Go 1.25+ requirement). R2 was right that this isn't an
*absorb* problem — it's a *replace* problem. A Claude Code user with no Go
toolchain reads it and bounces.

**Direction.** Replace the current section with a four-step bootstrap for the
target audience:

1. Install the binary (one of: install script, `brew install`, GitHub Release
   tarball, or — for Go users — `go install`).
2. `ask install-skill` (populates `~/.claude/skills/ask/`).
3. `claude mcp add --scope user ask -- ask mcp` (registers MCP server).
4. Start a new Claude Code session (or `/clear` the current one) to pick up
   the new skill. Verified: there is no in-session reload-skills command —
   Claude Code loads skills at session start. You don't need to quit and
   relaunch the app.
5. `ask init` in any repo to set up `.ask/`. Note that this appends `.ask/items/`
   to `.gitignore` so the user isn't surprised.

Add a "did it work?" green-light check: `ask version` and `ask list` in an
init'd repo, both before the user involves Claude Code (highest-friction part
of the loop to debug).

Cross-link from skill onboarding back to README setup. Add a one-liner to
`topLevelHelp`: `First time? See README.md for setup, or run ask install-skill`.

**Open questions.**
- Resolved. Single home: README Getting Started, fully rewritten.

## §3. `install-skill` UX when files are refused

**Problem.** When `install-skill` finds a destination file whose contents
differ from the embedded copy, it refuses and exits 1. The current per-file
`pass --force to overwrite` hint is easy to miss. Failure mode for new users:
"I upgraded ask, didn't notice the skill is stale, exit 1 looked like a
normal install error."

**Direction.**
- **Human output:** add a top-of-output summary line when `len(Refused) > 0`:
  `N file(s) modified locally; re-run with --force to overwrite, or diff
  against ~/.claude/skills/ask/ to merge.` "Modified locally" (not "differ from
  the bundled copy") makes clear which side is the user's.
- **Reload hint on success:** in `renderInstallSkillHuman`, on the *written*
  path only (skip when everything was `Skipped`), append:
  `note: restart Claude Code or reload skills for ~/.claude/skills/ask/ to take effect.`
- **JSON shape:** drop `omitempty` from `Refused` so consumers don't have to
  handle two shapes. Add a top-level `status` field with values `clean` |
  `partial` mirroring exit codes (0 / 1). Don't use `ok`/`error`; partial isn't
  an error.
- **Skip `--check` flag as its own thing.** R1 argued against it; R2 noted R1
  then reinvented it under §4 as a content-hash check. Land it once, in §4's
  PR, named `--check`.
- **Known limitation:** `--force` is all-or-nothing. No story yet for "user
  has hand-edited their local skill and wants to keep edits but pull new
  reference files." Name it in docs; defer until a real user asks.

**Open questions.**
- Resolved.

## §4. Version stamping + content-hash check

**Problem.** Once ask is distributed, drift between binary and installed skill
becomes a real failure mode. Two-pronged: (a) you can't tell by looking which
binary version an installed SKILL.md shipped with, and (b) you can't tell
whether the *current* installed skill is stale relative to the *current*
binary's embedded copy.

**Direction.** Ship both halves — they're cheap and complementary.

- **Stamp at build time.** Two-line change in `internal/skill/`:
  - Add `var Version = "dev"` in `skill.go`.
  - In the `FS()` walker and `SkillMD()` accessor, inject the version on read
    (not on disk). No codegen, no `go generate`, no template.
  - Makefile `-ldflags -X` gains one entry:
    `-X github.com/aac/ask/internal/skill.Version=$(VERSION)`.
  - Format: `vX.Y.Z+abc1234` (semver + short SHA). Build date as gravy.
- **Surface on success.** `install-skill` prints
  `ask install-skill v1.2.0 → ~/.claude/skills/ask` — free trust signal,
  self-describing in screenshots and logs.
- **`ask install-skill --check`.** Read-only mode: compare embedded SKILL.md
  hash (and every referenced file) against the installed copy. Exit 0 if all
  match, 1 if anything drifts. No writes. Use case: setup scripts, CI, an
  `ask doctor`-style command later. This is the same `--check` R1 first
  rejected and then reinvented in §3; land once, here.

**Settled (Andrew, 2026-05-18):** YAML frontmatter key. Add
`version: vX.Y.Z+abc1234` to SKILL.md's frontmatter. Visible via
`head -5 SKILL.md`; the skill loader ignores unknown keys.

## §5. LICENSE

**Problem.** Repo has no top-level LICENSE. Public distribution without one
is a legal mess — implicit copyright leaves users in an undefined
redistribution position. R1 missed this entirely; R2 named it.

**Direction.** Commit a LICENSE file before the first GitHub Release.

**Settled (Andrew, 2026-05-18):** Apache-2.0. The patent grant (vs MIT's
absence of one) is the load-bearing difference for a tool meant to spread —
protects adopters from a contributor later claiming a patent on the
technique. Matches the Go ecosystem default.

## §6. `.ask/items/` schema versioning

**Problem.** Once a second user adopts ask, the JSON shape in
`.ask/items/*.json` becomes a compatibility contract. Agents written against
v1.1 will break on v1.5 field renames. Cheaper to add a versioning field
*before* a single external user's store has to be migrated than to retrofit.

**Direction.** Add a `schema_version: "1"` field to every Item written to
disk. Read path tolerates missing field (treats as `"1"`). Migration story
deferred until a v1.x bumps it, but having the field in place makes the
migration story possible. Document in `docs/spec.md` that the field is
authoritative and that agents reading `.ask/items/` must check it.

**Open question.**
- Format: numeric `1`, semver `"1.0"`, or string `"1"`? Recommend string
  `"1"` — leaves room for `"1.1"` later without changing the type.

## §7. Uninstall path

**Problem.** `ask install-skill` writes to `~/.claude/skills/ask/`. Once §1
ships, MCP registration adds an entry to `~/.claude.json`. `rm` of the
binary alone leaves orphans in both. R1 dismissed this as "just docs"; R2
noted it's one paragraph of work that saves a real support question.

**Direction.** Two cheap options, pick one:
- **Docs only:** README "Uninstall" section listing the three steps:
  `rm <binary>`, `rm -rf ~/.claude/skills/ask/`,
  `claude mcp remove ask`. Simple, no new code.
- **Subcommand:** `ask install-skill --uninstall` removes the skill dir, and
  README documents the binary + MCP remove steps. Adds inverse symmetry to
  the install command but doesn't fully close the loop.

Recommend docs-only for v1.1. Revisit if a real user files the support
question.

**Open question.**
- Resolved (docs-only) unless Andrew wants the subcommand symmetry.

## §8. Discoverability

**Problem.** Both R1 and R2 implicitly assumed "user has found ask." The
wall *before* install is "how does a Claude Code user hear about ask at
all?" Even great install UX is wasted if no one shows up.

**Direction.** Not v1.1 engineering work, but a v1.1 *project* deliverable:
- Cross-link from the `act` README (the sibling tool that already has some
  audience).
- One-paragraph blog post or GitHub Discussion explaining the
  "agent-toolkit primitive" framing (ask + act + poke).
- Mention in the relevant Claude Code community channels.

**Open question.**
- Author's call on timing and channels.

## Out of scope for v1.1

- macOS code-signing / notarization (§0 defers).
- Linux package managers beyond install script (§0 defers).
- `ask doctor` health subcommand (`--check` plus the version stamp surface
  most of what doctor would do).
- Telemetry / phone-home (intentionally none for a CLI inbox; GitHub Release
  download counts are the proxy adoption signal).
- Schema migration framework (§6 lays groundwork; framework comes when
  someone needs to bump the version).
- "Hybrid edit" support for install-skill (§3 known limitation).

## Tickets

Existing tickets get description updates to reflect resolved questions:
- `act-cf60` — README Getting Started (§2; scope is *replace* not *absorb*).
- `act-d048` — install-skill UX (§3; resolved).
- `act-7b6f` — version stamp (§4; merged with content-hash check; one open
  question on stamp location).

New tickets to file post-review:
- §0 Binary distribution (GoReleaser + install script; Homebrew tap as
  follow-on).
- §1 MCP registration docs (one-line README change; trivial).
- §5 LICENSE commit.
- §6 `.ask/items/` schema_version field.
- §7 Uninstall docs section in README.

Discoverability (§8) is not an engineering ticket; tracked separately.
