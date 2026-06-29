# On-resume verification and orchestrator-checkpoint polling

`ask` has no event bus, no notifications, and no background processes — it's a state store. Agents drive everything around it by reading state at the right moments. This reference covers two of those moments: verifying resolved items on session resume, and checkpoint-polling in long work loops.

## Verifying resolved items on session resume

When you start work in an ask-enabled project, items in `status=resolved` are pending verification — the human said done, but no agent has run the verifier or closed yet. The mechanism is `ask list --status=resolved` followed by per-item handling from `ask help workflow` (run the verifier if attached, close on exit 0, reopen with captured output on nonzero).

But *when* to invoke this is an agent judgment call:

- Run it when about to do work that depends on the state an ask was filed about.
- Run it when the user explicitly asks about the inbox.
- Honor the orchestrator-checkpoint convention if you're running a long loop (below).
- Skip it when the current session has nothing to do with the area the asks cover.

If verification surfaces reopens (verifier failed, item is back in `open` with `verification_output` populated), surface those to the user *before* doing dependent work. The whole point of the reopen path is that the human gets to see the failure and react.

## Orchestrator checkpoints

If you're running a long work loop, poll `ask list --status=open --urgency=blocker` before claiming new work. A sibling agent (or the human via CLI) may have filed a blocker that should halt the loop. `ask help workflow` documents the full checkpoint convention, including:

- **Before claiming new work in a long loop:** `ask list --status=open --urgency=blocker` — if non-empty, halt or address.
- **At session start:** `ask list --status=resolved` — run verifiers and close/reopen before doing anything else.
- **Watching for a specific ask to close:** poll `ask show <id>` (or `ask list --status=closed`) at whatever cadence the loop tolerates.

ask is state-only; it has no event bus and no notifications. Polling is the integration model.
