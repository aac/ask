# Writing a callback handler

A subscription tells ask: when this item is resolved, closed or reopened, run this handler. This page is the guide to writing the handler. The contract it rests on is [`spec.md` §14](spec.md); `ask help callbacks` is the short form.

## When to use one

ask is pull-based: whoever is waiting on an item polls it. That is enough for the agent that filed the item and for a person reading their inbox. A callback is for a third party that would otherwise have to poll forever, for example a tool that posted a comment saying "this is waiting on a person" and wants to post the follow-up when the person answers.

A handler is told that an item changed. It decides nothing about the item, and nothing it does can fail or undo the change.

## The smallest handler

A handler is an executable file in `~/.config/ask/callbacks/`, on the host where the store lives. Its file name is its name, and must match `^[a-z0-9][a-z0-9_-]{0,63}$`.

This one records every change it is told about:

```sh
mkdir -p ~/.config/ask/callbacks
cat > ~/.config/ask/callbacks/record <<'EOF'
#!/bin/sh
# What ask hands a handler: a built environment and one JSON object on stdin.
env | sort > "$HOME/fire.env"
cat > "$HOME/fire.stdin"
echo recorded
EOF
chmod +x ~/.config/ask/callbacks/record
```

Subscribe it to an item and change the item:

```sh
$ ask subscribe ask-3c89 --on any --handler record --arg thread=abc
ask-3c89: subscribed 6d69ca27 (resolve,close,reopen -> record); status open
$ ask resolve ask-3c89 --note done
ask-3c89: open -> resolved
$ ask fires ask-3c89
fire cafbd927  item ask-3c89  subscription 6d69ca27  handler record
  2026-05-15T10:30:00Z  resolve  open -> resolved
  ok, exit 0 in 0.2 s
  output:
    recorded
```

`ask resolve` returned before the handler ran. `ask show ask-3c89` lists the subscription with a one-line account of each of its last three fires, and never the output.

## What the handler receives

**Working directory:** the project root, the directory that holds `.ask/`.

**Environment:** built by ask, not inherited. `~/fire.env` from the handler above holds these and, apart from what the shell adds for itself, nothing else:

```
ASK_ARG_THREAD=abc
ASK_AT=2026-05-15T10:30:00.123456Z
ASK_EVENT=resolve
ASK_FIRE_ID=cafbd927
ASK_FROM_STATUS=open
ASK_ID=ask-3c89
ASK_STORE=/path/to/project
ASK_SUB_ID=6d69ca27
ASK_TO_STATUS=resolved
HOME=/home/you
LANG=en_US.UTF-8
PATH=/usr/bin:/bin:/path/to/ask/dir:/home/you/.local/bin
```

- `ASK_EVENT` is `resolve` (open to resolved), `close` (anything to closed) or `reopen` (resolved to open).
- `ASK_ARG_<KEY>` carries each `--arg key=value` given at subscribe time, the key upper-cased. Arguments are how one handler serves many subscriptions: the subscriber says which thread, which channel, which record this item belongs to. At most 16 per subscription; keys match `^[a-z][a-z0-9_]{0,31}$`; values are at most 1024 bytes.
- `PATH` is short on purpose. Call anything outside it by its full path. `ask` itself is on it.
- Nothing from the process that changed the item reaches the handler: not its `PATH`, not its tokens, not its working directory. That process may be a person's shell, an agent's session or a long-lived server, and a handler must behave the same from all three.

**Standard input:** one JSON object.

```json
{
  "item":  { "schema_version": "1", "id": "ask-3c89", "title": "...", "status": "resolved", "resolution_note": "done", "...": "..." },
  "event": "resolve",
  "from":  "open",
  "to":    "resolved",
  "at":    "2026-05-15T10:30:00.123456Z",
  "args":  { "thread": "abc" }
}
```

`item` is the whole Item ([`spec.md` §1.1](spec.md)) as the store holds it when the handler starts. A handler that only needs the id and the event can ignore stdin and read the environment.

## What to rely on, and what not to

**It runs at most once, and is never retried.** Not after a failure, not after a timeout, not if the host went down while it ran. If losing one event matters, do not rely on the handler alone: keep whatever polled before as a slow backstop, or check `ask fires --json` for fires whose `state` is not `ok`.

**It has 60 seconds.** Then it and every process it started are killed. A handler that needs longer should hand the work to something that outlives it and return.

**Its output is stored and shown. Never print a secret.** The last 4096 bytes written to stdout and stderr are kept in `.ask/fires/<id>.jsonl` and printed by `ask fires` to anyone who can read the store. Print what a person debugging the handler will want (what it did, what it could not do) and nothing that should not be read. Do not run a handler under `set -x` when its commands carry credentials.

**Its exit code is its verdict.** `0` is recorded as `ok`, anything else as `failed` with the code. `ask show` puts that in front of whoever looks at the item.

**It may run more than once for one item.** An item can be resolved, reopened and resolved again, and each change fires. Make the handler safe to repeat, or key what it does on `ASK_FIRE_ID`, which is new for every fire.

**It may not be there.** A subscription names a handler on the host where the store lives. If the handler is removed, or the store is copied to a host that does not have it, the fire is recorded as `not_run` and `ask show` says `no handler named <name> on this machine`.

## Where a handler keeps its own settings

Every file in `~/.config/ask/callbacks/` is a handler, so nothing else belongs there. The convention for a handler's configuration and credentials is one file per handler outside it:

```
~/.config/ask/callback-config/<name>.env     # mode 0600
```

read by the handler itself:

```sh
#!/bin/sh
set -eu
. "$HOME/.config/ask/callback-config/notify.env"   # sets NOTIFY_URL, NOTIFY_TOKEN
/usr/bin/curl -fsS -o /dev/null -H "Authorization: Bearer $NOTIFY_TOKEN" \
  --data-urlencode "text=$ASK_ID: $ASK_EVENT" "$NOTIFY_URL"
echo "told $ASK_ARG_CHANNEL about $ASK_ID"
```

ask neither reads nor creates that directory. Keeping the secret in a file the handler reads, and out of `--arg`, matters: arguments are stored in the store in plain text.

## Checking on handlers

```sh
ask show <id>            # each subscription, and its last three fires in words
ask fires <id>           # every fire of the item, with the output
ask fires --json         # every fire in the store, for a script
```

What `ask show` can say under a subscription:

| Line | Meaning |
|---|---|
| `ok, exit 0 in 0.4 s` | the handler ran and exited 0 |
| `failed, exit 22 in 7.2 s` | it ran and exited non-zero; `ask fires` has its output |
| `not run: no handler named <name> on this machine` | no executable under that name when the item changed |
| `killed at the 60 s timeout` | it was still running at 60 seconds |
| `started, no result after <age> (it is running)` | it has not finished yet |
| `started, no result after <age> (it died; it is not retried)` | the run was cut off, for example by a reboot |
| `not fired yet: no <events> since this was added` | nothing has happened that the subscription names |
| `missed: <event> at <time> by a build that does not fire; not retried` | the item was changed by an ask from before callbacks, such as a long-running `ask mcp` server that has not been restarted since an upgrade |

## Removing a subscription

```sh
ask unsubscribe <id> <subscription-id>
```

The subscription id is the 8-character id `ask show` lists; any unambiguous prefix works. A closed item never changes again, so its subscriptions need no cleanup.
