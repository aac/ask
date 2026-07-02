---
name: ask
metadata:
  version: "0.2.1"
description: Use when you need a human to take an action an agent can't — set up an OAuth credential, sign into a service, place a file at a path the agent can't reach, give feedback on a UI — and the action outlives the chat the filing agent is in. Also trigger in any repo with a .ask/ directory at its root or an ancestor, when the user mentions "what's in my inbox", "file an ask", "ask request", or ask-XXXX ids, or when an agent uses ask MCP tools (ask_new, ask_list, ask_resolve, ask_reopen, ask_close). Covers the file-resolve-verify-close arc, when to file vs. ask in chat, verifier discipline, and orchestrator checkpoints. Also trigger when an agent generates follow-ups, next steps, or open items in a task, recap, or status report — each item needing a human action outside the chat should be an ask, not a chat bullet. Fire on phrasings like "next steps require the user", "blocked on a decision", "follow-ups need a call", "you'll need to", "the user should", "later you should", or any list of human-action items.
---

# ask — agent-to-human request inbox

ask is an agent-first request inbox: a single Go binary plus MCP server, for *agents requesting work from a human* — the actions an agent can't take itself. This skill is the usage layer — how to drive the tool. The mechanics live in `ask help`; this skill is the conventions and rules on top of them.

**Reference files (read when the situation applies):**

- `references/when-to-file.md` — anti-patterns, interrupt-budget heuristics, when an ask is overkill, when a question is masquerading as an ask, when to file two asks vs. one composite. Read before filing if you're unsure whether the thing in front of you is actually an ask.
- `references/verifier-recipes.md` — common verifier shapes for the v1 shell shape (exit code, file existence, HTTP probe, env-var presence). Read when attaching a verifier.
- `references/on-resume.md` — verifying resolved items on session resume, and orchestrator-checkpoint polling for long work loops. Read if you're picking up state from a prior session or running a loop.
- `references/feedback-patterns.md` — the same-session feedback shortcut and the feedback-destination pattern for fyi-shaped asks where human feedback is the deliverable.

## Bootstrap

Run `ask help` once at the start of any session in an ask-enabled project. The skill is opinions; `ask help` is mechanics. Without that, you'll have to guess flags and field names this skill assumes you know. If `ask help` fails (binary not on PATH), check whether `ask` ships bundled with this skill as a plugin before assuming it's missing: a launcher named `ask` is usually already on `$PATH`, otherwise look for `bin/ask` at the plugin root (a sibling of `skills/`, often `${CLAUDE_PLUGIN_ROOT}/bin/ask`, or located with `find / -path '*/bin/ask' -o -path '*/bin/ask-*' 2>/dev/null`). If only a platform-suffixed binary is present, invoke it by name — `ask-$(uname -s | tr 'A-Z' 'a-z')-$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)`. If the plugin filesystem is read-only, copy the whole `bin/` dir to a writable location and `chmod +x` first. Only if no bundled binary exists, follow the Installation and MCP registration steps in the [ask README](https://github.com/aac/ask#installation).

**Storage is local-only and per-machine, by design.** `.ask/` lives next to the project's working tree, is gitignored, and does not sync across machines or worktrees. Items the human sees on their laptop are not items a worktree subagent sees, and vice versa. This is load-bearing for three things downstream: the surface-via-report pattern for worktree/remote/CI subagents (§Dispatching subagents), recovery via `ask harvest --from <path>` when filesystem-shared, and the per-machine "interrupt budget" framing — the human's inbox is one machine's inbox, not the project's global queue.

## When to file vs. not

**Load-bearing first rule: never use ask for anything your harness's native in-conversation mechanisms (clarifying questions, structured-question primitives like Claude Code's `AskUserQuestion` or whatever the equivalent is in your environment) would serve.** If the user explicitly tells you to file an ask, file it — that's an instruction, not a judgment call. But the skill *auto-firing* on an ambient phrase (a follow-up list, a "you'll need to…") is not a commitment to file: if the right answer there is "ask in chat," do that. ask is for actions the human takes outside the conversation, that don't require synchronous in-conversation back-and-forth with the filing agent, and that some future agent can plausibly verify or resume against.

### When you are generating a list of follow-ups

**If you are about to emit a bullet list of "next steps," "open items," "follow-ups," or "things the user should do," apply the predicate below to each item before writing it to chat.** Items that pass the predicate belong in ask, not in a chat bullet — chat output is ephemeral; an ask survives the session and is visible to the next agent or session resume. Filing is automatic (see CLAUDE.md: "default to handling git yourself" generalizes: default to filing rather than surfacing in chat). The concrete trigger flows are:

- **End-of-session recaps and wrap-up.** Any session recap, wrap-up, or status-report output that lists human-action items. For each item, run the predicate; file the ask before emitting the recap. Print a summary line like "Filed N asks — use `ask list` to see them" at the end of the recap.
- **Plan-mode blockers.** When mapping out a multi-step plan and you discover a step that requires a human action before the next agent can proceed, file the ask now (urgency `blocker` if it's on the critical path) rather than adding it to the plan as a "you'll need to…" note.
- **Mid-task walls.** Anytime you say "you'll need to," "the user should," "later you should," "next steps require the user," or "blocked on a decision" — stop and file. The phrases are the signal; the ask is the action.

> **The single predicate: is your current iteration blocked on this answer?**
>
> - **Yes** — ask in chat (using whatever in-conversation question mechanism your harness provides). The human's response feeds straight back into your loop; filing is indirection.
> - **No** — it's ask-shaped. The consumer of the resolution is some *future* agent (possibly you in a later session, more often someone else), not the one currently typing. The human will get to it on their own clock.
>
> The wrong framing is "is this a question or an action?" — many ask-shaped items are decisions the human has to make on their own time. The right framing is *who consumes the answer, and when*.

A common miss: **human decisions where the next agent (not the filer) acts on the resolution are ask-shaped, not chat-shaped.** "Decide which architecture to pursue after you're back from travel and write the chosen path to `docs/decision.md`" looks like a question, but the current agent's iteration is not blocked on it — a future agent will read the file and proceed. File it. The phrase "to unblock the same agent" in the predicate above is doing the load-bearing work; if you only read "human decision = in-chat question" you'll miss this case.

Subsequent rules:

- Only file for actions that survive the filing agent's session.
- One ask per discrete action. If a single failure point has two distinct human steps (set up OAuth *and* invite a bot), that's two asks unless the steps are truly atomic.
- Provide enough context in `body` that some future agent — not the filer — can act on the resolution: URLs, file paths, env-var names, expected outcomes.
- Don't file what you can resolve yourself. If you can write the file, run the command, hit the API — do it. ask is for the boundary you literally cannot cross.

Minimal `ask new` invocation:

```sh
ask new "Set up GitHub OAuth app" \
  --body "Create an OAuth app at https://github.com/settings/developers.
Callback URL: http://localhost:3000/auth/callback.
Save client ID and secret to .env.local as GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET." \
  --blocks act-1234
```

`--blocks` is optional. When present, it names the tracker-issue id (e.g. `act-XXXX`) whose work is blocked pending resolution of this ask; ask stores it verbatim and never interprets it — the consumer is `ask list --blocks=<id>` and tooling like `act ready`, which excludes issues with unresolved blocking asks. Omit if the ask doesn't map to a specific tracker issue. Only `title` (positional) and `--body` are required.

When in doubt, check `references/when-to-file.md`.

## Urgency choice

Three values, with downstream consequences:

- `blocker` — orchestrators that respect this halt the current work loop after filing. Use only when a downstream consumer truly cannot proceed. Canonical example: "Set up Gmail OAuth at this URL and save credentials to `.env.local`" — the next agent on the import job literally cannot run without it.
- `normal` — the default. Orchestrator-judgment. Canonical example: "Invite the bot user to the Slack workspace so the digest job can post" — needed this week, not in the next ten minutes.
- `fyi` — for things the human can address when convenient. Orchestrators typically continue working. Canonical example: "A 10-minute data-quality improvement is waiting for a human eyeball — look at `tmp/dupes.csv` and mark which rows are real duplicates" — useful work for the human to pick up at a natural break, no downstream consumer is blocked.

Be honest. The urgency determines whether the human's day gets interrupted. A `blocker` you didn't really need is how trust in the urgency signal erodes.

## Verifier discipline

Attach a verifier when there's a programmatic check — a command that exits with a meaningful code, an HTTP probe, a file that should exist, an env var that should be set. Recipes in `references/verifier-recipes.md`.

Skip the verifier when the check is judgment-only ("does this UI look right?" — no shell command captures that). Resolution without a verifier is fine; the agent that picks up the resolved item just closes it directly per `ask help workflow`.

Be aware: a missing-command verifier (script renamed, project layout shifted) exits nonzero, triggers `reopen`, and surfaces the broken verifier to the human via `verification_output`. This is usually desirable — it means stale verifiers get caught and repaired on the next resolve cycle rather than rotting silently.

## Notes are optional

Resolving an item already encodes "the human said done." Most resolves should have no `--note`. Add a note only when there's context worth preserving: which path was chosen, what was partially completed, the content of feedback the human gave verbally. Don't restate "done" — `resolved_at` already says that.

## Agent-to-agent asks

ask is human-recipient by default. Items without a `recipient` field are aimed at the user of this inbox — the implicit single human. That covers almost every case the skill applies to.

When the actual consumer of an ask is *not* the implicit human — e.g. a peer agent polling its own inbox on a cron, a sibling worktree process, a service-account loop — set `--recipient <ref>` on `ask new`. The receiving agent runs `ask list --recipient <ref>` against the same inbox to scope its poll.

Recommended (non-binding) format: `<kind>:<identifier>`. Examples:

- `agent:data-prep` — an agent that polls its inbox on cron
- `agent:reviewer-bot` — a sibling agent process
- `human:alex` — an explicit human label when you want the contrast
- `team:reviewers` — a group label an orchestrator interprets

ask never interprets the format; it's an opaque tag. The kind prefix is a convention only — orchestrators that filter by prefix (e.g. "any `agent:*` ask") do so at their own discretion.

### When to set --recipient

- The consumer is another agent (a cron-driven loop, a sibling process, a vendor agent with its own polling). Set `--recipient agent:<name>` so it can scope its poll.
- The ask is one of several with distinct audiences in the same inbox, and the receivers need to disambiguate. Set the recipient on each.
- You're filing on behalf of a specific human in a multi-human surface and the distinction matters downstream (rare in v1; mostly forward-looking).

Skip it (omit the flag) for the common case: the implicit human picks the ask up via `ask list`. Adding a recipient where one isn't needed just adds clutter to `ask show`.

ask is pull-based: it doesn't deliver, route, or notify — the receiver polls with `ask list`. `resolve` / `reopen` / `close` behave identically regardless of recipient; it's just metadata.

## When you're not just filing

- **Resuming a session, or running a long work loop:** see `references/on-resume.md` for the resolve-verifier-close cycle and orchestrator-checkpoint polling.
- **Filing an fyi-feedback ask, or coordinating the human's chat for someone else's filed feedback:** see `references/feedback-patterns.md` for the same-session shortcut and the feedback-destination pattern.

**Idempotent no-op exits 6, not 0.** An idempotent no-op means the verifier ran, found the condition already satisfied, and made no change (e.g. `ask resolve` on an already-resolved item). `ask resolve` on an already-resolved item, `ask close` on an already-closed item, and `ask reopen` on an item not in `resolved` all succeed-as-no-ops with exit code 6 (and a stderr warning). The state is correct; the call just didn't change anything. Scripts under `set -e` will trip on this — orchestrator loops polling for state should treat exit 6 as success (equivalent to 0) or branch explicitly. The success-shape payload (id or item JSON on stdout) is still emitted.

## Dispatching subagents (surface-via-report)

"Subagent" here covers whatever your harness calls a dispatched, isolated agent run — Claude Code subagents (Task tool / dispatched worktree agents), Codex `spawn_agent` reports, autonomous loops in remote dev environments, CI jobs. The pattern is harness-agnostic.

`.ask/` is single-machine, per-working-tree, gitignored. A subagent running in a worktree, a remote dev environment (sculptor, Codespaces, Coder), or CI sees its *own* `.ask/` — the one the human runs `ask list` against on their laptop is somewhere else entirely. If the subagent calls `ask new` directly there, the item is invisible to the human until something copies it across.

The default model avoids that gap entirely. **Subagents surface; orchestrators file.** This works uniformly across worktree, remote-dev, and CI topologies because it doesn't depend on filesystem sharing.

### Pattern

When you dispatch a subagent that might hit a human-action wall, instruct it to *not* call `ask new`. Instead it ends its report with an `## Asks` section. The orchestrator (the dispatching agent in the human's main session) parses the block and files into the human's main inbox.

### Payload shape (in the subagent's report)

Minimal one-entry block (title and body are the only required fields):

```markdown
## Asks

- title: Set up GitHub OAuth app
  body: |
    Create an OAuth app at https://github.com/settings/developers.
    Callback URL: http://localhost:3000/auth/callback.
    Save client ID and secret to .env.local as GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET.
```

With optional fields (urgency, verifier, blocks):

```markdown
## Asks

- title: Set up Gmail OAuth for the import job
  urgency: blocker
  blocks: act-1234  # Optional. An act-XXXX tracker-issue id whose work is blocked pending resolution of this ask.
  body: |
    Create an OAuth client at https://console.cloud.google.com/...
    with scopes [gmail.readonly]. Save credentials to .env.local as
    GMAIL_OAUTH_CLIENT_ID and GMAIL_OAUTH_CLIENT_SECRET.
  verifier: pnpm test:gmail-auth

- title: Confirm the new design copy reads correctly
  urgency: fyi
  body: |
    Open http://localhost:3000/onboarding and write feedback to
    docs/review-notes.md.
```

Required fields per entry: `title`, `body`. Optional: `urgency` (defaults to `normal`), `verifier` (a shell command string for a v1 shell verifier), `blocks` (Optional — an opaque tracker-issue ref identifying which tracker issue is blocked pending resolution of this ask. The value is an `act-XXXX` id in this project. ask stores it verbatim and never interprets it; the consumer is `ask list --blocks=<id>` and downstream tooling like `act ready`, which excludes issues with unresolved blocking asks. Omit if the ask doesn't map to a specific tracker issue.). The exact serialization is the orchestrator's choice — YAML-ish blocks, JSON, a simple `### Ask N` heading-per-item, whatever the orchestrator's parser expects. The shape above is illustrative; the load-bearing thing is *that the subagent surfaces these fields explicitly, and does not call `ask new` itself.*

### Dispatch-prompt template

When dispatching a subagent into any isolated environment, include a paragraph like:

> If you hit a human-action wall during this work (an OAuth credential to set up, a service to sign into, a UI to click through, a file to place at a path you can't reach), **do not call `ask new`**. Surface it via an `## Asks` section at the end of your report. One entry per discrete action. Required: `title`, `body`. Optional: `urgency` (`blocker` | `normal` | `fyi`; default `normal`), `verifier` (shell command), `blocks` (Optional — the `act-XXXX` id of the tracker issue whose work is blocked pending resolution of this ask, if any; omit if there is none). Body should give a future agent everything it needs to act on the resolution: URLs, file paths, env-var names, expected outcomes. Surfacing happens at report time; the human won't see asks during your run.

Tune the prose to the dispatching surface (act task, plan step, CI job), but the rules — don't call `ask new`, use the `## Asks` block, one ask per action, sufficient body context — are constant.

### Orchestrator protocol on receiving asks

When the subagent's report comes back, for each `## Asks` entry:

1. **File** with `ask new` using the surfaced fields. If `blocks: <tracker-issue-id>` is present, pass `--blocks <tracker-issue-id>` (repeatable for multi-issue asks).
2. **For blocker-urgency asks tied to a specific tracker issue:** register the reciprocal external-dep on that tracker issue using *your tracker's external-dep mechanism*. ask doesn't know how this is spelled — `act update --add-dep`, `gh issue edit`, a beads custom-field write, a note in the issue body — all equivalent. Project-specific commands belong in the project's orchestrator doc, not in this skill.
3. **Unclaim/park** the blocked tracker issue so the ready-queue stops surfacing it. The blocker halts *the blocked unit of work*, not the whole orchestrator loop — the loop keeps dispatching unrelated issues; only the parked one waits for resolution.
4. **On a resume checkpoint:** for each ask that closed since the last checkpoint, read its `blocks` list and clear the reciprocal external-dep on each named tracker issue, returning them to the ready-queue. `ask list --status=closed --blocks=<issue-id>` is the targeted query for per-issue polling.

The reciprocal edge lives entirely in this protocol — ask carries `blocks` as opaque strings, the tracker carries the inverse as opaque strings, neither tool reads the other.

### Partial-work decisions are out of scope

If a subagent reaches a blocker mid-task with partial commits in its worktree branch, what happens to that branch — discard, persist a ref as a tracker note, leave it for the resume agent to inherit — is the orchestrator's call. ask doesn't prescribe and neither does the tracker; different orchestrators choose differently. The blocker-halts-the-blocked-unit semantics give the orchestrator the freedom to make this choice without ask getting in the way.

### Harvest as fallback

`ask harvest --from <path> [--clean]` still exists. Use it when:

- A subagent (one not following this skill — older prompts, autonomous loops, a different agent vendor) called `ask new` directly into its own `.ask/` and the items need to come across before its working directory is gone.
- A filesystem-shared dispatch (worktree, NFS mount, shared volume) and you'd rather copy than re-file.
- Recovery from a dropped report — the subagent surfaced asks but the orchestrator never received the report cleanly; if its `.ask/` is still on disk and reachable, harvest pulls in what would otherwise be lost.

`ask harvest --from .claude/worktrees/agent-XXX --clean` copies every item under that path's `.ask/items/` into the current store and removes the source items so re-running is a no-op. Pre-flight collision detection means a clean copy or a clear refusal — never a partial mutation. Run it before the directory is pruned; once the worktree (or remote machine, or CI runner) is gone the items are lost. 

Harvest is the *fallback*; surface-via-report is the default. If you find yourself wiring harvest into a regular orchestrator loop, that's a signal the dispatch prompt isn't telling subagents to surface.
