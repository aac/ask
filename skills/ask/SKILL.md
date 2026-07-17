---
name: ask
metadata:
  version: "0.2.1"
description: Use when you need a human to take an action an agent can't — set up an OAuth credential, sign into a service, place a file at a path the agent can't reach, give feedback on a UI — and the action outlives the chat the filing agent is in. Also trigger in any repo with a .ask/ directory at its root or an ancestor; when the user mentions "what's in my inbox", "what do you need from me", "anything blocking you", "file an ask", "ask request", or ask-XXXX ids; or when an agent uses ask MCP tools (ask_new, ask_list, ask_resolve, …). Also trigger when an agent generates follow-ups, next steps, or open items — each such item should be an ask, not a chat bullet. Fire on phrasings like "next steps require the user", "you'll need to", "the user should", or any list of human-action items. Also trigger — even with no .ask/ directory anywhere — whenever a human-needed item exists and no store exists yet; initialize one with ask init rather than routing it into a handoff doc, recap, or chat-only note.
---

# ask — agent-to-human request inbox

ask is an agent-first request inbox (a Go binary + MCP server): agents file the actions they can't take themselves for a human to do. This skill is the conventions layer; the mechanics live in `ask help`.

**Reference files (read when the situation applies):**

- `references/when-to-file.md` — anti-patterns and judgment heuristics; read before filing if unsure the thing is actually an ask.
- `references/verifier-recipes.md` — verifier shapes for the v1 shell verifier (exit code, file existence, HTTP probe, env-var).
- `references/on-resume.md` — verifying resolved items on resume, and orchestrator-checkpoint polling in long loops.
- `references/feedback-patterns.md` — the same-session feedback shortcut and the feedback-destination pattern for fyi-feedback asks.

## Bootstrap

Run `ask help` once at the start of any session in an ask-enabled project — it carries the mechanics (flags, field names) this skill assumes. If it fails (binary not on PATH), the tool likely ships bundled: look for `bin/ask` at the plugin root (`${CLAUDE_PLUGIN_ROOT}/bin/ask`), or `find / -path '*/bin/ask' -o -path '*/bin/ask-*' 2>/dev/null`. A platform-suffixed binary is invoked by name: `ask-$(uname -s | tr 'A-Z' 'a-z')-$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)`. If the plugin dir is read-only, copy `bin/` somewhere writable and `chmod +x`. Only if no bundled binary exists, follow the [ask README](https://github.com/aac/ask#installation).

**Storage is local-only and per-machine.** `.ask/` lives next to the working tree, is gitignored, and does not sync across machines or worktrees — the human's inbox is one machine's inbox. This is why dispatched subagents surface asks rather than filing them (§Dispatching subagents), and why `ask harvest` exists for filesystem-shared recovery.

## When to file vs. not

**Never use ask for anything your harness's native in-conversation mechanisms (clarifying questions, `AskUserQuestion` or the equivalent) would serve.** If the user tells you to file an ask, file it — that's an instruction. But the skill auto-firing on an ambient phrase (a follow-up list, a "you'll need to…") is not a commitment to file: if the right answer is "ask in chat," do that.

**The single predicate: is your current iteration blocked on this answer?**

- **Yes** — ask in chat. The human's response feeds straight back into your loop; filing is indirection.
- **No** — it's ask-shaped. The consumer is a *future* agent (possibly you, once the human has stepped away), acting on their own clock. File it.

The framing is *who consumes the answer, and when* — not "question vs. action." A human decision that a future agent (not you) acts on is ask-shaped even though it reads like a question: "decide the architecture after travel and write it to `docs/decision.md`" is an ask, because your current iteration isn't blocked on it. Conversely, if the human isn't present to answer synchronously, file rather than lose the item in chat.

Apply this to every "next steps," "open items," "follow-ups," or "you'll need to…" list before writing it to chat — recaps, wrap-ups, plan-mode blockers, mid-task walls. Items that pass the predicate go in ask, not a chat bullet (chat is ephemeral; an ask survives). If no `.ask/` store exists yet, `ask init` and file — don't fall back to a handoff doc or chat note.

Rules:

- Only file for actions that outlive the current window of human interaction (when the human steps away), not merely the filing agent's session.
- One ask per discrete action. Two distinct human steps (set up OAuth *and* invite a bot) are two asks unless truly atomic.
- Put enough in `body` that a future agent — not the filer — can act on the resolution: URLs, file paths, env-var names, expected outcomes.
- Don't file what you can resolve yourself. If you can write the file, run the command, hit the API — do it. ask is for the boundary you literally cannot cross.

Minimal invocation (only `title` and `--body` required):

```sh
ask new "Set up GitHub OAuth app" \
  --body "Create an OAuth app at https://github.com/settings/developers.
Callback URL: http://localhost:3000/auth/callback.
Save client ID and secret to .env.local as GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET." \
  --blocks act-1234
```

`--blocks` is optional: it names the tracker-issue id (`act-XXXX`) whose work is blocked pending this ask. ask stores it verbatim and never interprets it; the consumer is `ask list --blocks=<id>` and tooling like `act ready`. Omit if the ask maps to no tracker issue.

## Urgency

`blocker | normal | fyi`. Be honest — urgency decides whether the human's day gets interrupted, and a needless `blocker` erodes trust in the signal.

- `blocker` — orchestrators halt the blocked work loop after filing. Only when a downstream consumer truly cannot proceed.
- `normal` — the default. Needed soon, not this minute.
- `fyi` — address when convenient; orchestrators keep working.

## Verifiers

Attach `--verifier` when there's a programmatic check — a command with a meaningful exit code, an HTTP probe, a file that should exist, an env var that should be set. Recipes in `references/verifier-recipes.md`. Skip it when the check is judgment-only (does the UI look right); resolving without a verifier is fine. A missing-command verifier exits nonzero and reopens, surfacing stale verifiers to the human — usually desirable.

## Notes

Resolving already encodes "the human said done." Add `--note` only for context worth preserving (which path was chosen, what was partial, verbal feedback) — don't restate "done."

## Agent-to-agent asks

ask is human-recipient by default; items without `--recipient` target the implicit human. When the consumer is another agent (a cron-driven loop, a sibling process, a service-account poll), set `--recipient <kind>:<identifier>` so it can scope its poll with `ask list --recipient <ref>`. Examples: `agent:data-prep`, `agent:reviewer-bot`, `human:alex`, `team:reviewers`. ask never interprets the tag — it's opaque; the kind prefix is convention only. Skip the flag for the common single-human case. ask is pull-based (no delivery or notification); `resolve`/`reopen`/`close` behave identically regardless of recipient.

## Idempotent no-ops exit 6

`ask resolve` on an already-resolved item, `ask close` on an already-closed one, and `ask reopen` on an item not in `resolved` succeed as no-ops with exit code 6 (plus a stderr warning; the normal stdout payload is still emitted). Scripts under `set -e` should treat 6 as success (equivalent to 0).

## Dispatching subagents (surface-via-report)

"Subagent" = any dispatched, isolated run: a worktree agent, a remote dev environment (sculptor, Codespaces, Coder), a CI job. Its `.ask/` is its own — invisible to the human, who runs `ask list` elsewhere. So **subagents surface; orchestrators file.**

Instruct the dispatched subagent to *not* call `ask new`. Instead it ends its report with an `## Asks` section that the orchestrator (in the human's main session) parses and files:

```markdown
## Asks

- title: Set up Gmail OAuth for the import job
  urgency: blocker
  blocks: act-1234
  body: |
    Create an OAuth client at https://console.cloud.google.com/...
    with scopes [gmail.readonly]. Save credentials to .env.local as
    GMAIL_OAUTH_CLIENT_ID and GMAIL_OAUTH_CLIENT_SECRET.
  verifier: pnpm test:gmail-auth
```

Per entry, required: `title`, `body`. Optional: `urgency` (default `normal`), `verifier` (shell command), `blocks` (`act-XXXX` tracker id, omit if none). The serialization is the orchestrator's choice; the load-bearing thing is that the subagent surfaces these fields explicitly and does not call `ask new` itself.

**Dispatch-prompt template** — include a paragraph like:

> If you hit a human-action wall (an OAuth credential to set up, a service to sign into, a UI to click through, a file to place where you can't reach), **do not call `ask new`**. Surface it in an `## Asks` section at the end of your report — one entry per discrete action, required `title` and `body`, optional `urgency`/`verifier`/`blocks`. Give a future agent everything it needs to act on the resolution: URLs, file paths, env-var names, expected outcomes.

**Orchestrator protocol on receiving asks** — for each entry:

1. File with `ask new`, passing `--blocks <id>` for each surfaced `blocks`.
2. For blocker asks tied to a tracker issue, register the reciprocal external-dep on that issue via your tracker's mechanism (e.g. `act update --add-dep`), and unclaim/park the issue so the ready-queue stops surfacing it. The blocker halts the blocked unit of work, not the whole loop.
3. On a resume checkpoint, for each ask closed since the last one, clear the reciprocal dep on its `blocks` issues to return them to the queue. `ask list --status=closed --blocks=<id>` is the per-issue query.

**Harvest fallback.** `ask harvest --from <path> [--clean]` copies items from another `.ask/` into the current store (collision-checked; `--clean` removes the source so re-runs no-op). Use it when a subagent called `ask new` directly and its items must come across before its directory is pruned, or for filesystem-shared dispatch. Surface-via-report is the default; wiring harvest into a regular loop signals the dispatch prompt isn't telling subagents to surface.
