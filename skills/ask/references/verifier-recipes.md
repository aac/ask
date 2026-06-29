# Verifier recipes — common shapes for `ask new --verifier`

v1 ships shell verifiers only (the schema reserves `url` and `mcp` types but doesn't implement them). A verifier is a shell command that an agent runs on resume when an item is in `status=resolved`; exit 0 means verified, exit nonzero triggers `reopen` with stdout+stderr captured in `verification_output`.

Execution semantics, from the brief:

- cwd is the directory containing `.ask/` (the project root).
- env is inherited from the calling agent process.
- `timeout_seconds` is advisory — the agent running the verifier is expected to honor it; ask itself doesn't enforce it in v1.
- A missing command (script renamed, binary not on `PATH`) exits nonzero and reopens — this is the intended way for stale verifiers to surface back to the human.

Below are the four most common shapes. Each section: when it's the right shape, the recipe, and what to watch out for.

## 1. Exit-code check on a test or script

**When:** there's an existing test, lint, type-check, or script that fails when the human action wasn't taken and passes when it was. The cleanest verifier shape — defer to a thing that already exists.

```sh
ask new "Set up Gmail OAuth" \
  --verifier 'pnpm test:gmail-auth' \
  --body "..."
```

Or:

```sh
ask new "Update Stripe webhook secret" \
  --verifier 'go test ./internal/payments -run TestWebhookSig' \
  --body "..."
```

**Watch out for:**

- Test isolation. If the test reads env vars or filesystem state set up *during the same test*, the verifier may pass even when the human's prior step didn't land. Prefer tests that read the actual artifact the human was asked to produce.
- Flaky tests. A verifier that fails 1-in-20 will reopen items the human already resolved correctly. If the underlying test is flaky, either fix the test first or pick a different verifier shape.
- Tests that need a running service. If the test command starts/expects a service, see the HTTP-probe recipe — sometimes a direct probe is cleaner than a wrapping test.

## 2. File existence (or non-emptiness)

**When:** the human's action produces a file at a known path — a credentials file, a generated config, a written feedback doc.

```sh
ask new "Save Gmail OAuth credentials" \
  --verifier 'test -s .env.local && grep -q GMAIL_OAUTH_CLIENT_ID .env.local' \
  --body "Save credentials from the OAuth console to .env.local as GMAIL_OAUTH_CLIENT_ID and GMAIL_OAUTH_CLIENT_SECRET."
```

```sh
ask new "Write design review feedback" \
  --urgency fyi \
  --verifier 'test -s docs/review-notes.md' \
  --body "Open http://localhost:3000 and write feedback to docs/review-notes.md."
```

**Watch out for:**

- `test -e` vs. `test -s`. `-e` passes for empty files; `-s` requires non-zero size. For a feedback file the human is supposed to *write*, prefer `-s` — an empty file is usually not what you wanted.
- Path relativity. cwd is the project root (the directory containing `.ask/`). Paths in the verifier should be relative to that root, not to wherever the human was working.
- Content checks via `grep`. Useful for "this file exists *and* has the expected key in it," but keep the pattern loose enough to survive formatting variation (whitespace, key casing).

## 3. HTTP probe via curl

**When:** the human action results in a service being available, an OAuth callback working, a webhook endpoint reachable. Direct probes are sometimes cleaner than a test that wraps the same probe.

```sh
ask new "Start the dev server" \
  --verifier 'curl -fsS http://localhost:3000/health > /dev/null' \
  --body "Run pnpm dev in a separate terminal."
```

```sh
ask new "Verify ngrok tunnel is live" \
  --verifier 'curl -fsS -o /dev/null -w "%{http_code}" https://my-tunnel.ngrok.app/webhook | grep -q 200' \
  --body "Start ngrok pointing at localhost:3000."
```

**Watch out for:**

- `-f` (fail on HTTP error) is essential — without it, curl returns 0 for a 500 response. Add `-s` (silent) and `-S` (show errors on failure) so the captured output is useful if it fails.
- The server has to already be running when the verifier runs. If the human action is "start the dev server," the verifier passes *only* during the session where they kept it running. That's usually right for a tunnel-or-localhost kind of ask but wrong if you wanted persistence — file a file-existence ask instead (e.g. a launchd plist).
- Authenticated endpoints. If the probe needs a token, that token has to live in the inherited env. Document the dependency in the ask body so a future agent knows which env to source.

## 4. Env-var presence

**When:** the human action is "set this environment variable" or "source this dotenv file in your shell rc." Simple, direct, no filesystem indirection.

```sh
ask new "Set GMAIL_OAUTH_CLIENT_ID in shell env" \
  --verifier 'test -n "$GMAIL_OAUTH_CLIENT_ID"' \
  --body "Export GMAIL_OAUTH_CLIENT_ID from .env.local into your shell (e.g. add 'set -a; source .env.local; set +a' to your zshrc)."
```

```sh
ask new "Provision STRIPE_SECRET_KEY" \
  --verifier 'test -n "$STRIPE_SECRET_KEY" && [[ "$STRIPE_SECRET_KEY" == sk_* ]]' \
  --body "..."
```

**Watch out for:**

- Inheritance scope. The verifier runs in whatever shell the agent invokes — `bash -c` for most agents. Env vars set only in an interactive shell's startup file (`~/.bashrc`, `~/.zshrc`, or equivalent) won't be inherited by a non-login `bash -c` invocation. Prefer asks that produce a *file* the next agent can source, then verify that file's existence (file-existence recipe).
- Prefix/format checks (`[[ "$X" == sk_* ]]`). Useful for catching a placeholder value the human pasted without replacing. Use sparingly — false positives on legitimate values are annoying.
- The env-var-presence shape is the *least* portable across resume contexts. A future agent in a fresh shell with no `.env` sourced will see the var missing and reopen the item. If the var should persist across sessions, the underlying ask is really "write this to a file my future shell will source" — file-existence verifier on that file is cleaner.

## Choosing a shape

Rough order of preference, when more than one fits:

1. **Exit-code check on an existing test** — defers to a real check, fails meaningfully when the upstream behavior breaks.
2. **File existence / non-emptiness** — most resumable; the artifact persists across sessions.
3. **HTTP probe** — for transient services, accept the "only works when running" failure mode and document it in the body.
4. **Env-var presence** — last resort; prefer a file the next agent can source.

If none of these fit, the action is probably judgment-only (a design review with no written deliverable) — skip the verifier and resolve directly when the human says done.
