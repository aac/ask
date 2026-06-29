package cli

// helpTopics holds the long-form prose printed by `ask help <topic>`.
// Each entry is plain text intended for terminal display. The dispatcher
// in help.go exits 2 when an unknown topic is requested.
var helpTopics = map[string]string{
	"workflow": workflowTopic,
	"verifier": verifierTopic,
	"mcp":      mcpTopic,
	"schema":   schemaTopic,
}

const workflowTopic = `ask help workflow — state transitions and the agent contract

ask is a state store. Items move through three states: open, resolved,
closed. Every CLI verb (and every MCP tool) is one transition on the
state machine. ask itself never runs verifiers, never polls, and never
notifies — agents and orchestrators drive everything around it.

States
  open      Filed and awaiting human action.
  resolved  The human said done; no agent has verified or closed yet.
  closed    Final. Verifier passed, or the item was cancelled/dismissed.

Transition verbs
  ask new <title> [flags]    Files a new item. Status starts as open.
  ask resolve <id> [--note]  open -> resolved. Clears verification_output.
  ask reopen <id> [--reason] resolved -> open. Reason is captured as
                             verification_output (it is the verifier's
                             stdout+stderr on the failure path).
  ask close <id> [--reason]  open|resolved -> closed. From resolved this
                             is the normal "verified, done" path. From
                             open this is cancel/dismiss; --reason is
                             recommended in that case and populates
                             resolution_note.

Cross-store move
  ask harvest --from <path>  Copy every item under <path>/.ask/items/
                             into the current store. Used by orchestrators
                             that dispatch subagents into git worktrees:
                             each worktree gets its own gitignored .ask/,
                             so items filed there are invisible to the
                             main checkout until harvested. Pre-flight
                             checks refuse the call on id collision or
                             when source==target; --clean removes source
                             items after a successful copy so re-running
                             is a clean no-op.

Transition matrix
  From         resolve                       reopen                       close
  open         -> resolved (clears v_out)    no-op                        -> closed (cancel/dismiss; reason -> resolution_note)
  resolved     no-op                         -> open (reason -> v_out)    -> closed (preserves verified_at)
  closed       error                         error (re-file is canonical) no-op

One-shell example
  $ ask new "Set up Gmail OAuth" --urgency=blocker \
      --verifier 'pnpm test:gmail-auth'
  ask-3c89
  $ ask resolve ask-3c89        # human said done
  $ pnpm test:gmail-auth        # an agent runs the verifier
  $ ask close ask-3c89          # exit 0 -> close
  # if the verifier exited nonzero, instead:
  $ ask reopen ask-3c89 --reason "$(pnpm test:gmail-auth 2>&1)"

Idempotency
  Any verb in its target state returns exit 6 with a stderr warning and
  the success-shape on stdout (id or item). Scripts that care can branch
  on exit 6; scripts that don't can treat 0 and 6 as equivalent.

Verifier cwd/timeout semantics (for the agent that runs the verifier)
  - cwd: the directory containing .ask/ (the project root for the ask
    inbox, which may not be the agent's cwd at the moment of resume).
  - env: inherits the calling process's environment.
  - timeout_seconds: advisory. ask v1 stores it but does not enforce it;
    the agent that executes the command is expected to honor it.
  - missing command: a verifier whose command no longer exists exits
    nonzero like any other failure and triggers reopen. The shell error
    lands in verification_output — usually the desired affordance,
    because it surfaces the broken verifier to the human.

Orchestrator checkpoint convention
  ask has no event bus. Orchestrators that want to react to ask state
  must poll. The recommended cadence:
    - Before claiming new work in a long loop:
        ask list --status=open --urgency=blocker
      If non-empty, halt or address.
    - At session start:
        ask list --status=resolved
      Run any attached verifiers and close/reopen before doing other
      work. Surface reopens to the human before proceeding with anything
      dependent on the original resolution.
    - When watching a specific item to close: poll ask show <id> (or
      ask list --status=closed) at whatever cadence fits the loop.
  These are conventions, not behaviors ask enforces. If an orchestrator
  skips them, ask's state stays consistent; the orchestrator just won't
  react.
`

const verifierTopic = `ask help verifier — when, why, and the v1 shell shape

A verifier is an optional, agent-executed programmatic check that
confirms a human-reported resolution is real. ask itself never runs
verifiers in v1; it stores the schema and exposes the verbs. The agent
that picks up a resolved item runs the verifier and reports pass/fail by
calling ask close or ask reopen.

When to attach a verifier
  Attach when a programmatic check exists that meaningfully confirms the
  outcome: a test command, an HTTP probe, a file existing where the
  human was supposed to put it, a credentials path that becomes
  readable. Skip when the only check is judgment ("does this UI feel
  right?", "did we agree on the right plan?") — let the human's resolve
  be authoritative.

  A non-trivial verifier is cheap insurance: if the human resolves but
  the artifact is missing or broken, the verifier reopens the item with
  the failure captured. Without a verifier, the next agent might proceed
  on a false resolution.

The v1 shell shape
  Verifiers in v1 have one type: "shell". The schema is:

    {
      "type":            "shell",
      "command":         "pnpm test:gmail-auth",
      "timeout_seconds": 60
    }

  The reserved values "url" and "mcp" are explicitly rejected by ask new
  validation in v1 so future versions can claim them.

  Execution contract (for the agent that runs the command):
    - cwd: the directory containing .ask/.
    - env: the calling process's environment, unmodified.
    - shell: the user's interactive shell ($SHELL, or sh as fallback).
      ask does not invoke the command itself; the agent passes the
      string to the shell.
    - timeout_seconds: advisory. ask stores it; agents are expected to
      honor it. Default is 60 when a verifier is supplied without an
      explicit timeout.
    - On timeout: treat as nonzero exit; the captured output should
      include a marker noting the timeout (e.g. "[timeout after 60s]")
      and ask reopen --reason is called with that text.

Three recipes

  1. Command exit code (a test, a lint, a build)

       ask new "Set up Gmail OAuth" \
         --verifier 'pnpm test:gmail-auth'

     The command exits 0 iff the auth flow works; nonzero on any
     failure. This is the most common shape.

  2. File existence (a credential, a generated artifact)

       ask new "Drop service-account.json into config/" \
         --verifier 'test -s config/service-account.json'

     Use test -s (file exists AND is non-empty), not test -e — an empty
     file is almost always wrong. test -s exits 0 on a non-empty regular
     file, 1 otherwise. The agent will reopen with "stat: ..." style
     output captured.

  3. HTTP probe via curl

       ask new "Bring staging API back online" \
         --verifier 'curl -fsS https://staging.example.com/health'

     -f makes curl exit nonzero on HTTP 4xx/5xx; -s silences progress;
     -S still shows errors on stderr. The verifier passes only when the
     endpoint returns 2xx. For probes needing a specific body, pipe
     through grep:
       curl -fsS https://example.com/health | grep -q '"ok":true'

Missing-command behavior
  If the verifier shell command is missing (binary renamed, project
  layout shifted, path changed), the shell exits nonzero (typically
  127), the agent captures the "command not found" message, and ask
  reopen lands that text in verification_output. The human sees the
  reopen with the broken verifier as the failure note, and the canonical
  fix is: ask close <stale-id> --reason "verifier stale" and ask new a
  fresh item with the corrected verifier. ask v1 has no edit verb.

A verifier that returns 0 but no longer tests what it used to is a
silent failure ask cannot detect. Verifier maintenance is the human's
job, like any other test in the project.
`

const mcpTopic = `ask help mcp — MCP tools and an example transcript

ask mcp starts a stdio JSON-RPC 2.0 server speaking MCP protocol
2024-11-05. Server name: ask-mcp. The server exposes one tool per CLI
verb except init (init is a developer action, not an agent action).
Tool shapes mirror the CLI flags; semantics are identical in both
directions.

Flags
  --workdir <path>   chdir to <path> before serving. Useful on Codex
                     Desktop / Codex VS Code where the MCP server's cwd
                     is not the user's workspace (upstream bugs
                     openai/codex#9989, #16390). The Codex CLI sets the
                     cwd correctly; --workdir is the IDE/Desktop escape
                     hatch.

Tools

  ask_new
    Args:
      title       (required) string, 1..200 chars
      body        string, 0..16384 chars
      urgency     "blocker" | "normal" | "fyi"   (default "normal")
      filed_by    string, <=256 chars
      recipient   string, 1..1024 chars, non-whitespace; opaque
                  agent-to-agent label (e.g. agent:data-prep,
                  human:andrew, team:reviewers). Absent = implicit
                  human. ask never interprets the format.
      tracker_ref string, <=256 chars
      verifier    { type:"shell", command:string,
                    timeout_seconds:int 1..3600 }
      links       array of { label, url }, max 32 entries
      blocks      string[] of opaque cross-tool refs (each 1..1024
                  chars, non-whitespace); ask never interprets the
                  format. Typically act-XXXX ids.
    Returns: the created Item object.

  ask_list
    Args:
      status     string[] subset of ["open","resolved","closed"]
      urgency    string[] subset of ["blocker","normal","fyi"]
      blocks     string (single ref); filters to items whose blocks
                 array contains this ref (exact match)
      recipient  string (single ref); filters to items whose
                 recipient field equals this ref. Items without a
                 recipient never match an explicit filter.
      all        boolean (mutually exclusive with status)
    Returns: array of Item objects in display order (urgency desc,
    then created_at asc, then id asc). Empty array when none match.
    Default filter (when status omitted and all is not true): status
    != closed.

  ask_show
    Args: id (required) — any non-empty hex prefix, with or without
    the "ask-" prefix.
    Returns: the Item object.

  ask_resolve
    Args: id (required), note (optional string).
    Returns: the post-transition Item object.

  ask_reopen
    Args: id (required), reason (optional string). Reason is captured
    as verification_output (verifier failure output, typically).
    Returns: the post-transition Item object.

  ask_close
    Args: id (required), reason (optional string). From resolved this
    is the normal verified-done path. From open this is cancel/dismiss
    and reason is recommended (it lands in resolution_note).
    Returns: the post-transition Item object.

Response envelope
  Every successful call returns:
    { "content": [{ "type":"text", "text":"<json>" }],
      "isError": false }
  The text field is the same JSON the CLI emits with --json. Agents
  JSON-parse the text.

Error envelope
  On any non-success outcome:
    { "content": [{ "type":"text",
                    "text":"{\"code\":3,\"message\":\"ask-9999: not found\"}" }],
      "isError": true }
  code mirrors the CLI exit-code taxonomy (2,3,4,5). Idempotent no-ops
  (code 6) come back as isError:false with the item payload plus an
  extra text element containing the warning — the call succeeded.

Example transcript

  -> { "jsonrpc":"2.0", "id":1, "method":"initialize",
       "params":{ "protocolVersion":"2024-11-05" } }
  <- { "jsonrpc":"2.0", "id":1,
       "result":{ "protocolVersion":"2024-11-05",
                  "serverInfo":{"name":"ask-mcp","version":"0.1.0"} } }

  -> { "jsonrpc":"2.0", "id":2, "method":"tools/call",
       "params":{ "name":"ask_new",
                  "arguments":{
                    "title":"Set up Gmail OAuth",
                    "urgency":"blocker",
                    "verifier":{ "type":"shell",
                                 "command":"pnpm test:gmail-auth",
                                 "timeout_seconds":60 } } } }
  <- { "jsonrpc":"2.0", "id":2,
       "result":{ "content":[{"type":"text",
                              "text":"{\"id\":\"ask-3c89\",\"title\":\"Set up Gmail OAuth\",...}"}],
                  "isError":false } }

  -> { "jsonrpc":"2.0", "id":3, "method":"tools/call",
       "params":{ "name":"ask_list",
                  "arguments":{ "status":["open"], "urgency":["blocker"] } } }
  <- { "jsonrpc":"2.0", "id":3,
       "result":{ "content":[{"type":"text",
                              "text":"[{\"id\":\"ask-3c89\",...}]"}],
                  "isError":false } }
`

const schemaTopic = `ask help schema — Item fields and project config

ask stores state as JSON files under .ask/. The on-disk shape and the
--json output shape are the same object; every field is always present
(no omitempty), with null for unset optionals.

Per-repo layout
  .ask/
    config.json          # project identity
    items/
      ask-3c89.json
      ask-7ecd.json

.ask/ is added to .gitignore by ask init when a git repo is present.
Items are per-user, per-machine — they do not sync via git.

Item object (.ask/items/ask-XXXX.json)

  Field                Type            Notes
  id                   string          "ask-" + 4 hex chars, lowercase.
                                       sha256(project_id || created_at
                                       || title)[0:2] in hex. Collisions
                                       resolved by +1ns bump on
                                       created_at, up to 1024 retries.
  title                string          1..200 chars after trim, no
                                       newlines. Required.
  body                 string          0..16384 chars. Default "".
  urgency              string          blocker | normal | fyi. Default
                                       normal. ask itself does nothing
                                       with urgency beyond storing it;
                                       orchestrators read it.
  status               string          open | resolved | closed.
  filed_by             string | null   Free-form, <=256 chars. By
                                       convention: "<agent-kind>:
                                       <session-id>". Optional.
                                       Consumer: agents debugging "where
                                       did this ask come from?" — e.g.
                                       picking which session transcript
                                       to grep. Not surfaced in ask list
                                       output; read via ask show or
                                       --json. Set it when the filing
                                       session is non-obvious; skip when
                                       it isn't.
  recipient            string | null   Free-form, 1..1024 chars,
                                       non-whitespace. Optional
                                       agent-to-agent label naming who
                                       should pick the ask up.
                                       Absent (null) = implicit human
                                       (the user of this .ask/).
                                       Recommended format:
                                       "<kind>:<id>" — agent:data-prep,
                                       human:andrew, team:reviewers.
                                       ask never interprets the format;
                                       orchestrators do (typically by
                                       prefix-scoping their poll).
                                       Filter: ask list --recipient
                                       <ref>. The receiving agent's
                                       canonical pattern is a cron
                                       poll: ask list --recipient
                                       agent:<name> --status open.
  tracker_ref          string | null   Free-form cross-reference (act
                                       id, beads, GH#, ...). <=256.
                                       Consumer: agents correlating an
                                       ask back to the work item that
                                       motivated it ("what was act-3c89
                                       trying to do when it filed this?")
                                       and humans reading ask show. ask
                                       never interprets the string — no
                                       linkification, no fetch. Not
                                       surfaced in ask list. The newer
                                       blocks field carries the
                                       *forward* edge (ask → tracker
                                       issue this ask blocks); tracker_ref
                                       is the *backward* "who filed this"
                                       pointer. They overlap when an
                                       agent files an ask for the issue
                                       it's currently working on.
  verifier             object | null   See below.
  links                array           Array of { label, url } objects.
                                       Always present, [] when none.
                                       Max 32 entries; label 1..120,
                                       url 1..2048, url not validated
                                       beyond non-empty.
                                       Consumer: agents rendering ask
                                       show to a human, who may render
                                       url as clickable. "Open this URL
                                       and click X"-shaped asks should
                                       populate links so the human can
                                       click instead of copy-pasting
                                       from body. CLI ingest: today
                                       links are JSON-ingest-only (set
                                       via MCP ask_new or by writing the
                                       item JSON directly); ask new has
                                       no --links flag in v1.
  blocks               array           Array of opaque cross-tool refs
                                       (strings) this ask is blocking.
                                       Always present, [] when none.
                                       Each ref 1..1024 chars,
                                       non-whitespace. ask never
                                       interprets the format;
                                       orchestrators do (typically an
                                       act-XXXX id, but any string).
  resolution_note      string | null   Set by resolve --note or by close
                                       --reason from open/resolved.
                                       Preserved across subsequent
                                       transitions. <=16384.
  verification_output  string | null   Verifier output captured by
                                       reopen --reason. Cleared on
                                       resolve. <=65536, truncated with
                                       "\n[truncated]\n" if longer.
                                       Presence + status=="open" is the
                                       "came back with an error"
                                       condition a UI renders.
  created_at           string          RFC 3339 UTC, "Z" suffix,
                                       nanosecond precision when
                                       nonzero. Required.
  resolved_at          string | null   RFC 3339 UTC. Set on transition
                                       to resolved.
  verified_at          string | null   RFC 3339 UTC. Set exactly once,
                                       only when a verifier exited 0.
                                       Preserved across later
                                       transitions as a historical
                                       record.
  closed_at            string | null   RFC 3339 UTC. Set on every
                                       transition to closed.

Verifier object

  Field            Type     Notes
  type             string   v1 accepts only "shell". "url" and "mcp"
                            are reserved and rejected by ask new.
  command          string   1..4096 chars. The agent passes this to
                            the user's shell; ask never executes it
                            in v1.
  timeout_seconds  integer  1..3600. Default 60 when a verifier is
                            supplied without an explicit timeout.
                            Advisory; ask does not enforce.

Link object

  { "label": "OAuth console",
    "url":   "https://console.cloud.google.com/..." }

ProjectConfig (.ask/config.json)

  {
    "project_id":   "01HXYZ...",                // ULID, generated on
                                                // init. Opaque, stable
                                                // across renames/moves.
    "display_name": "inbox-triage",             // defaults to repo
                                                // basename; editable.
    "created_at":   "2026-05-15T10:30:00Z"
  }

project_id is the input to the id hash and is the only stable handle on
a project. display_name is what UIs render; renaming the directory does
not change project_id.
`
