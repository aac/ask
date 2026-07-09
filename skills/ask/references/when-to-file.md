# When to file an ask — anti-patterns and judgment heuristics

Unpacks the skill's load-bearing rule (**never use ask for what your harness's native in-conversation mechanisms would serve**): shapes where filing is wrong, and heuristics for the ambiguous middle.

## The interrupt budget

Every ask the human eventually sees is an interrupt — they re-encounter items the next time they pick up a project, usually mid-something-else, not at a triage queue. Model it as a small daily interrupt budget per project: spend a `blocker` on something that genuinely halts progress, a `normal` on something needed this week, an `fyi` freely — but stale fyis pile up and erode trust in the inbox.

If you're filing two asks in one session for the same project, check whether one is actually a clarifying question for chat, or whether they should be combined (see "Two asks vs. one composite").

## Anti-patterns

### Questions masquerading as asks

The most common error. Examples that look like asks but are actually in-conversation questions:

| Looks like | Actually is | Tool |
|---|---|---|
| "Confirm the OAuth flow you want" | A question with two answers | in-conversation |
| "Let me know which env vars to set" | A question awaiting a list | in-conversation |
| "Should I rename `fooBar` to `foo_bar`?" | A yes/no question | in-conversation |
| "Did you mean Slack or Discord?" | A disambiguation | in-conversation |
| "Tell me which database to use" | A choice from a small set | in-conversation |

The tell: **if the agent needs a synchronous answer to continue *its own current task*, it's a question, not an ask.** ask is for actions the human takes *outside the conversation* (browser, another app, filesystem). If the human's response would be typed back into the same chat to unblock the same agent, file nothing — ask in chat.

A subtler tell: if the body of the proposed ask is mostly "please tell me X" with no action verb, you're filing a question. Rewrite it as a question for the user, or close-the-loop on whatever else you can do without the answer.

**But "ask in chat" assumes the human is actually present to answer.** The rule above is "don't file when a synchronous in-chat answer will unblock you *right now*" — it is *not* "never file questions or decisions." If you're running autonomously, or the human has moved on, or it's a decision they'll make later in a different session, a question with no synchronous answer doesn't resolve gracefully — it just gets lost. In that case file the ask even though it reads like a question: the consumer of the answer is a *future* session, which is exactly the ask-shaped case. When the human isn't there to answer, filing is the safer failure mode — better a question parked in the inbox than a blocker that evaporates with the chat.

### Things you can do yourself

If you can write the file, run the command, hit the API, or otherwise make the change — do it. ask is for the boundary you literally cannot cross. Examples that are tempting but wrong:

- "Please run `pnpm install`" — you can run it (modulo permissions; if permissions block it, that's the actual ask, not the install).
- "Please update the README" — you can write to README.md directly.
- "Please commit this" — you can commit unless permissions block it.

If permissions are the blocker, file the *permission grant* as the ask, not the action you'd take afterward. Be specific about which command needs allowlisting.

### Tracker acceptance criteria that need the human

If you are working through a tracker (act, beads, Linear, GitHub Issues, anything), and a ticket has an acceptance criterion that requires the human to do something — run a command, check a UI, confirm a configuration — that criterion is broken if the human doesn't read tracker tickets. Common case: the human filed the ticket via you, doesn't browse the tracker themselves, and the agent who later claims the ticket has no path to close it.

The fix: file an **ask** whose verifier checks the human action, and use `--blocks <ticket-id>` (or the equivalent on the tracker's external-dep mechanism) to couple them. The ticket stays out of the ready queue until the ask resolves; the ask is the surface the human actually reads. The agent's job becomes "file the ask, wait for it to resolve, then close the ticket" — concrete and finishable.

The deeper rule this expresses: **anything an agent needs from the human to close their work is an ask, not a tracker criterion.** Even when the agent is the author dogfooding their own tool. The test isn't "is this the author?" — it's "can the agent close their work without bothering the human?" If no, file an ask.

### Asks that aren't verifiable or resumable later

ask is for actions some future agent can plausibly verify or resume against. If the action has no observable trace (the human "thought about it"), and no future session can pick up where you left off, you're probably in question territory or in "this work doesn't need ask" territory. Either ask in chat or just note it in whatever doc the work is captured in.

### Status-update asks

"Please review the PR I just opened" with no action verb beyond "review" and no destination for feedback is borderline. If the human reviewing is part of the same chat flow, it's a question. If you genuinely need them to leave a session, look at the PR in GitHub, and come back later — that's an ask, but write the body to make the action concrete: which PR, what specifically to look for, where to write feedback if it isn't being left on the PR itself.

### Asks the filer is also coordinating

If the agent filing the ask is *also* the agent currently coordinating the human's chat, the same-session feedback shortcut applies: don't file. Take the feedback in conversation and write it to the destination directly. Filing-and-resolving in one session is indirection with no benefit; it just adds two lines to the audit trail.

## Two asks vs. one composite

A real failure point sometimes maps to several distinct human steps. The judgment call: file one ask with a composite body, or file two atomic asks?

**File two when:**

- The steps can be done in either order or by different humans.
- Each step has its own verifier.
- The failure modes are independent (OAuth setup can succeed while bot invite fails).
- A future agent on resume might need to act on one without the other.

**File one composite when:**

- The steps are sequential and one is meaningless without the other ("create the OAuth client *and* copy the secrets to `.env.local`").
- A single verifier covers both.
- Splitting would force the human to triage two items that are actually one piece of work in their head.

When in doubt, file two. Atomic asks are easier to reopen with targeted failure context; composite asks fall back to "something in here didn't work" when verifiers fail.

## Heuristic checklist before filing

1. Could I just ask the user this in chat right now and get unblocked? If yes, do that.
2. Could I do this myself if a permission were granted? If yes, the ask is the permission grant.
3. Will a future agent, with no memory of this session, be able to act on the resolved state from `body` alone? If no, expand the body or rethink.
4. Is the urgency honest? Would I be annoyed at *myself* for filing this as `blocker` if I were the human?
5. Is this one discrete action, or am I bundling? If bundling, split unless the verifier is shared.

If all five pass, file. If any fail, revisit.
