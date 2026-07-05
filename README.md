# ask — Agent→Human Request Inbox

A single-binary inbox designed for AI coding agents to file requests for actions only a
human can take — approve a charge, sign into a service that needs your 2FA, hand over a
secret the agent can't read, or say whether the new UI actually feels right. Items live as
plain JSON files in `.ask/` inside any project. No server, no database, no schema setup.
Sibling to [act](https://github.com/aac/act): where act is *agents tracking work for
themselves*, `ask` is *agents requesting work from a human*.

```
$ ask list
ask-3c89  blocker  open       V  Approve the production deploy
ask-7ecd  fyi      open           Eyeball the new design at localhost:3000
```

## Why this exists

Agent loops fail at one consistent boundary: the human action the agent literally cannot
take. A chat session usually *does* surface what it needs at the moment it's blocked — but
if the human moves on without acting on it, the request is gone. It's now the human's job
to remember, or it's written down somewhere unstructured that has to be rediscovered later.
There's no durable, cross-project place for "things an agent needs a human to do." That's
the gap.

`ask` shares act's shape. act gave agents a tracker tuned to their workflow — atomic claim,
JSON-everywhere CLI, MCP-in-binary, a narrow command surface — instead of retrofitting
Linear or Jira. ask is that shape pointed the other way: an inbox tuned to the human
*receiving* agent requests — quick to file, quick to resolve, no separate dashboard to
operate, and not a replacement for the chat you already use. Same building blocks (single
binary, JSON-everywhere, MCP-in-binary, self-teaching help), narrowed for the
file → resolve → verify → close loop.

That shared shape runs to the interface, too: ask's surface is **agent-first**. It's
lightweight and meant to be driven *collaboratively with an agent* — the human says "what's
in my inbox?" or "done with the OAuth one" in chat, and the agent runs the `ask` commands.
You rarely type `ask` yourself; the agent is the primary operator on both sides.

## Quick start

You don't run ask as a daily driver yourself; you install the plugin and your agent drives
it. In **Claude Code**:

```text
/plugin marketplace add aac/ask
/plugin install ask@ask
```

The plugin bundles the `ask` binary, the workflow skill, and the MCP server. Then, in any
git repo:

```sh
ask init      # creates .ask/ and gitignores the per-item store
ask help      # tutorial; `ask help workflow` for the file → resolve → verify → close loop
```

## What a session looks like

```sh
# Agent hits a wall it can't get past.
$ ask new "Set up Gmail OAuth" \
    --body "Create an OAuth client at https://console.cloud.google.com/... with scopes [gmail.readonly]. Save credentials to .env.local as GMAIL_OAUTH_CLIENT_*" \
    --urgency blocker \
    --verifier 'pnpm test:gmail-auth' \
    --blocks act-3c89
ask-3c89

# Human, later, in chat with an agent in the same project: "what's in my inbox?"
$ ask list --status=open
ask-3c89  blocker  Set up Gmail OAuth (verifier attached)

# Human does the thing and says "done" in chat. The agent runs the attached
# verifier right then:
#   pass → ask close ask-3c89
#   fail → ask reopen ask-3c89 --reason "<verifier output>" and tell the human what's still off
$ ask resolve ask-3c89 && pnpm test:gmail-auth && ask close ask-3c89
```

`ask` is the state store. The agent decides when to file, when to verify, what to do on
failure; `ask` enforces the state machine and stays out of the way.

## How agents use this

`ask` exposes its full surface as an [MCP](https://modelcontextprotocol.io) server
(`ask mcp`, stdio transport). MCP tools mirror the CLI one-to-one — `ask_new`, `ask_list`,
`ask_resolve`, `ask_reopen`, `ask_close`, `ask_show` — so any MCP-aware agent (Claude Code,
Codex, custom SDK apps) can drive the loop through tool calls instead of shelling out. The
CLI is always available as the base surface: every operation is an `ask` subcommand, so any
agent that can run a shell command drives the full loop with no extra setup.

An opinionated skill ships with the plugin and installs to `skills/ask/SKILL.md`. It fires
when `.ask/` is present, when the user mentions phrases like "what's in my inbox" or
`ask-XXXX` ids, or when the agent is already using ask MCP tools. The skill layers
discipline (when to file vs. ask in chat, urgency honesty, verifier hygiene, the
feedback-destination pattern) on top of `ask help`'s mechanics.

## Installing

Installing the plugin is the canonical path — it bundles the binary, the skill, and the
MCP server. ask is built for **Claude Code** and **Codex**, the two harnesses where the
plugin and its MCP server work today:

- **Claude Code:** `/plugin marketplace add aac/ask`, then `/plugin install ask@ask`.
- **Codex:** `codex plugin marketplace add aac/ask`, then `codex plugin add ask@ask`. The
  Codex manifest points at the bundled skill (`./skills/`) and MCP server config
  (`./.mcp.json`); Codex launches the MCP server from the active project root.
- **No plugin manager?** Point your agent at this repo (`github.com/aac/ask`) and let it
  install whatever way fits its environment.

Cowork, the Claude Desktop app, and claude.ai aren't supported hosts yet: they can't launch
the plugin's MCP server the way the CLI harnesses do. Support for them is a planned
addition, not a requirement for anything above.

### Codex without the plugin

For Codex CLI, install the skill from a repo clone — copy `skills/ask/` into
`~/.codex/skills/ask/`, or symlink it with
`ln -s <path-to-ask-clone>/skills/ask ~/.codex/skills/ask`. Register MCP by copying this
repo's `.mcp.json` into the target project — Codex reads `.mcp.json` from the project root
on startup:

```json
{
  "mcpServers": {
    "ask": {
      "command": "ask",
      "args": ["mcp"]
    }
  }
}
```

`ask mcp` uses its process working directory as the repo root — whichever project the
launcher starts it in is the `.ask/` the MCP tools operate on. There is no `--workdir`
flag. The binary makes no network calls and needs no credentials beyond filesystem access.

### First inbox

Once `ask` is installed (plugin or binary), initialize an inbox in any repo:

```sh
cd <any project>
ask init                # creates .ask/ and gitignores the per-item store
```

Sanity-check the binary directly before involving an agent — it's the easiest part of the
loop to debug:

```sh
ask version             # prints the binary version
ask list                # in an init'd repo, prints "(no items)" — that's the green light
```

If both work, ask the agent in a new session something like *"what's in my inbox?"* — the
skill should fire and the agent should reach for `ask_list`. For the canonical
file → resolve → verify → close loop, run `ask help workflow`.

## Privacy / telemetry: none

`ask` is fully local — no analytics, no phone-home, no auto-update. The only network calls
are git operations the user explicitly invokes.

## Status

`ask` is pre-v1 but in daily use across a range of projects. The CLI, MCP server, skill,
and install path are all in place; the remaining work before release is packaging and docs
polish.

Design & internals: [`docs/spec.md`](docs/spec.md) is the authoritative implementation
contract — field types, error shapes, exit codes, JSON shapes.

## Convergence test: ask + act cross-tool protocol

[`scripts/smoke-external-dep.sh`](scripts/smoke-external-dep.sh) is the canonical
convergence test for the surface-via-report protocol (see `docs/brief.md` →
Blocker-handling protocol). It exercises both binaries end-to-end against an isolated
tempdir: files an `ask` with `--blocks <act-id>`, registers the reciprocal
`act update --ext-add`, asserts `act ready` excludes the blocked issue, resolves the ask,
clears the dep, and asserts the issue returns to `act ready`. The script self-cleans on
exit, runs no network ops, and fails fast with a named step on any drift. Run it after
rebuilding either binary to confirm the handshake still holds.

## Uninstall

Remove the plugin through your agent's plugin manager. If you installed the `ask` binary
directly instead, delete it from your `PATH` (`which ask` shows where it lives).

For a non-plugin Codex setup, also remove `~/.codex/skills/ask/` and the `ask` entry from
each project's `.mcp.json`.

Per-project `.ask/` directories are left in place — they're project data, not install
state. Remove them by hand from any repo where you no longer want an inbox.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
