# Feedback-shaped asks — same-session shortcut and feedback-destination pattern

Some asks request the human's *feedback* rather than an action (review a design, sanity-check a doc, look at a UI). The "deliverable" is a human's words, not a filesystem change, so they have their own patterns.

## Same-session feedback shortcut

If the agent filing an fyi-feedback ask is *also* the agent coordinating the human's chat right now, **don't file** — take the feedback in conversation and write it to the destination directly. The filing-and-resolving-in-one-session pattern is silly indirection: you'd file, then immediately resolve, then close, all within the same chat. Just take the feedback and write it.

The feedback-destination pattern (below) is for the case where the chat-coordination role is picked up later — by a *different* agent, or by a *later iteration of the same long-running agent* (a background worker that files the ask, keeps working, and later notices it resolved) — and whoever picks it up needs to know where to deliver feedback the filing turn didn't take synchronously.

## Feedback-destination pattern

For fyi-style asks where the human's feedback is the deliverable, specify the destination in the `body`:

```
Start the dev server (e.g. `pnpm dev`), open http://localhost:3000, and tell me
whether the new design looks right. Write your feedback to docs/review-notes.md.
```

Whoever later coordinates the human's chat — a different agent, or the same long-running agent polling the destination on a heartbeat — reads the body, takes the feedback in chat, writes it to the destination, then resolves. The filing turn doesn't block waiting on it.

If a verifier makes sense for the destination, attach one — `test -s docs/review-notes.md` is the simplest shape (see `references/verifier-recipes.md` for the family). Without a verifier, the chat-coordinating agent just resolves directly when the feedback is written.
