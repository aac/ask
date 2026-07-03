#!/usr/bin/env bash
# smoke-external-dep.sh — convergence test for the cross-tool surface-via-report protocol.
#
# Exercises ask + act end-to-end: file an ask with --blocks <act-id>, register
# the reciprocal external-dep on the act issue with --ext-add, assert act ready
# excludes the blocked issue, resolve the ask, clear the dep with --ext-rm,
# assert act ready includes it again.
#
# Runs in a throwaway tempdir (git init + act init + ask init). Cleans up on
# exit via trap. Idempotent across re-runs (each run uses a fresh tempdir).
#
# Exit codes:
#   0 — every assertion passed; the protocol is converged end-to-end.
#   1 — an assertion failed; the failing step is named.
#   2 — a prerequisite feature is missing in the installed ask/act binaries.
#   3 — environment problem (missing binary, can't init git/act/ask, etc).

set -u
set -o pipefail

SCRIPT_NAME=$(basename "$0")
TMPDIR_ROOT=""

# ---- output helpers ---------------------------------------------------------

log()  { printf '[smoke] %s\n' "$*"; }
ok()   { printf '[smoke]   OK %s\n' "$*"; }
fail() {
  printf '[smoke] FAIL (%s): %s\n' "${STEP:-?}" "$*" >&2
  exit 1
}
missing() {
  printf '[smoke] missing feature: %s\n' "$*" >&2
  printf '[smoke] rebuild/reinstall the affected binary and retry.\n' >&2
  exit 2
}
envfail() {
  printf '[smoke] environment error: %s\n' "$*" >&2
  exit 3
}

cleanup() {
  if [[ -n "$TMPDIR_ROOT" && -d "$TMPDIR_ROOT" ]]; then
    rm -rf "$TMPDIR_ROOT"
  fi
}
trap cleanup EXIT INT TERM

# ---- pre-flight: binaries and feature detection ----------------------------

STEP="pre-flight: binaries on PATH"
command -v ask >/dev/null 2>&1 || envfail "ask binary not on PATH"
command -v act >/dev/null 2>&1 || envfail "act binary not on PATH"
command -v git >/dev/null 2>&1 || envfail "git not on PATH"

STEP="pre-flight: ask --blocks flag on new + list"
# Capture --help output into a variable so SIGPIPE from `grep -q` cannot
# trip pipefail. (grep exits 0 on match, kills the producer with SIGPIPE,
# producer exits 141, pipefail propagates the 141 — confusing failure mode.)
ASK_NEW_HELP=$(ask new --help 2>&1 || true)
ASK_LIST_HELP=$(ask list --help 2>&1 || true)
ACT_UPDATE_HELP=$(act update --help 2>&1 || true)
ACT_DEPADD_HELP=$(act dep add --help 2>&1 || true)

case "$ASK_NEW_HELP"   in (*-blocks*)  ;; (*) missing "ask new --blocks (file the ask with a cross-tool ref)" ;; esac
case "$ASK_LIST_HELP"  in (*-blocks*)  ;; (*) missing "ask list --blocks (filter by blocked ref)" ;; esac

STEP="pre-flight: act dep add --external (add) + act update --ext-rm (clear) flags"
# act-ce1427 unified external-blocker *adds* under `act dep add --external`
# (symmetric with --blocked-by); removal stays on `act update --ext-rm`.
case "$ACT_DEPADD_HELP" in (*-external*) ;; (*) missing "act dep add --external (register external dep)" ;; esac
case "$ACT_UPDATE_HELP" in (*-ext-rm*)   ;; (*) missing "act update --ext-rm (clear external dep)"  ;; esac

ok "binaries present and advertise the new flags"

# ---- scratch environment ----------------------------------------------------

STEP="setup: create tempdir + git init"
TMPDIR_ROOT=$(mktemp -d -t ask-smoke-XXXXXX) || envfail "mktemp failed"
cd "$TMPDIR_ROOT" || envfail "cannot cd into tempdir $TMPDIR_ROOT"

git init -q .                                          || envfail "git init failed"
git -c user.email=smoke@local -c user.name=smoke \
    commit --allow-empty -q -m "smoke: init"           || envfail "initial commit failed"

STEP="setup: act init"
# Worktree-isolated scratch environment; act init no longer accepts --no-commit.
act init >/dev/null 2>&1 || envfail "act init failed in tempdir"

STEP="setup: ask init"
ask init >/dev/null 2>&1 || envfail "ask init failed in tempdir"

ok "scratch tempdir initialized at $TMPDIR_ROOT"

# ---- step 1: create the scratch act issue ----------------------------------

STEP="1: create scratch act issue"
ACT_OUT=$(act create --no-commit -p 2 "smoke: scratch issue" \
    --description "scratch issue used by smoke-external-dep.sh; safe to delete" 2>&1) \
    || fail "act create exited nonzero: $ACT_OUT"

# Output is "Created act-XXXX \"...\""; parse the id.
SCRATCH_ACT_ID=$(printf '%s\n' "$ACT_OUT" | grep -oE 'act-[a-f0-9]+' | head -n1)
[[ -n "$SCRATCH_ACT_ID" ]] || fail "could not parse scratch act id from: $ACT_OUT"
ok "created $SCRATCH_ACT_ID"

# Sanity: it should appear in act ready before we attach any dep.
STEP="1: sanity check: scratch act issue is in act ready pre-block"
READY_JSON=$(act ready --json 2>&1) || fail "act ready --json failed: $READY_JSON"
printf '%s' "$READY_JSON" | grep -q "\"$SCRATCH_ACT_ID\"" \
    || fail "$SCRATCH_ACT_ID not in 'act ready' before any ext-dep is attached: $READY_JSON"
ok "$SCRATCH_ACT_ID appears in 'act ready' as expected"

# ---- step 2: file the scratch ask with --blocks <scratch-act-id> -----------

STEP="2: ask new --blocks $SCRATCH_ACT_ID --urgency blocker"
SCRATCH_ASK_ID=$(ask new "smoke: scratch ask" \
    --urgency blocker \
    --blocks "$SCRATCH_ACT_ID" \
    --body "scratch ask used by smoke-external-dep.sh; safe to delete" 2>&1) \
    || fail "ask new exited nonzero: $SCRATCH_ASK_ID"

# Output is just the id on stdout.
SCRATCH_ASK_ID=$(printf '%s' "$SCRATCH_ASK_ID" | tr -d '[:space:]')
[[ "$SCRATCH_ASK_ID" == ask-* ]] || fail "ask new returned unexpected id: '$SCRATCH_ASK_ID'"
ok "created $SCRATCH_ASK_ID"

# Intermediate assertion: ask show reports the blocks ref.
STEP="2: ask show $SCRATCH_ASK_ID --json reports blocks=[$SCRATCH_ACT_ID]"
ASK_JSON=$(ask show "$SCRATCH_ASK_ID" --json 2>&1) \
    || fail "ask show --json failed: $ASK_JSON"
printf '%s' "$ASK_JSON" | grep -q "\"$SCRATCH_ACT_ID\"" \
    || fail "ask show does not list $SCRATCH_ACT_ID in blocks: $ASK_JSON"
ok "ask show confirms blocks ref"

# Intermediate assertion: ask list --blocks filter surfaces the new ask.
STEP="2: ask list --blocks $SCRATCH_ACT_ID surfaces $SCRATCH_ASK_ID"
LIST_JSON=$(ask list --blocks "$SCRATCH_ACT_ID" --json 2>&1) \
    || fail "ask list --blocks --json failed: $LIST_JSON"
printf '%s' "$LIST_JSON" | grep -q "\"$SCRATCH_ASK_ID\"" \
    || fail "ask list --blocks=$SCRATCH_ACT_ID did not include $SCRATCH_ASK_ID: $LIST_JSON"
ok "ask list --blocks filter resolves to $SCRATCH_ASK_ID"

# ---- step 3: register the reciprocal external dep --------------------------

STEP="3: act dep add $SCRATCH_ACT_ID --external $SCRATCH_ASK_ID"
EXT_ADD_OUT=$(act dep add "$SCRATCH_ACT_ID" --external "$SCRATCH_ASK_ID" --no-commit 2>&1) \
    || fail "act dep add --external exited nonzero: $EXT_ADD_OUT"
ok "external dep $SCRATCH_ASK_ID attached to $SCRATCH_ACT_ID"

# ---- step 4: assert act ready excludes the blocked issue -------------------

STEP="4: act ready excludes $SCRATCH_ACT_ID while ext-dep is live"
READY_JSON=$(act ready --json 2>&1) || fail "act ready --json failed: $READY_JSON"
if printf '%s' "$READY_JSON" | grep -q "\"$SCRATCH_ACT_ID\""; then
    fail "$SCRATCH_ACT_ID still in 'act ready' despite ext-dep $SCRATCH_ASK_ID: $READY_JSON"
fi
ok "$SCRATCH_ACT_ID correctly excluded from 'act ready'"

# ---- step 5: resolve the scratch ask ---------------------------------------

STEP="5: ask resolve $SCRATCH_ASK_ID"
RESOLVE_OUT=$(ask resolve "$SCRATCH_ASK_ID" 2>&1) \
    || fail "ask resolve exited nonzero: $RESOLVE_OUT"
ok "ask resolved: $RESOLVE_OUT"

# ---- step 6: clear the reciprocal external dep -----------------------------

STEP="6: act update --ext-rm $SCRATCH_ASK_ID $SCRATCH_ACT_ID"
EXT_RM_OUT=$(act update --ext-rm "$SCRATCH_ASK_ID" --no-commit "$SCRATCH_ACT_ID" 2>&1) \
    || fail "act update --ext-rm exited nonzero: $EXT_RM_OUT"
ok "external dep cleared"

# ---- step 7: assert act ready includes the issue again ---------------------

STEP="7: act ready includes $SCRATCH_ACT_ID again after ext-rm"
READY_JSON=$(act ready --json 2>&1) || fail "act ready --json failed: $READY_JSON"
printf '%s' "$READY_JSON" | grep -q "\"$SCRATCH_ACT_ID\"" \
    || fail "$SCRATCH_ACT_ID did not return to 'act ready' after ext-rm: $READY_JSON"
ok "$SCRATCH_ACT_ID restored to 'act ready'"

# ---- done -------------------------------------------------------------------

log "OK: surface-via-report protocol converges end-to-end."
log "    scratch ask: $SCRATCH_ASK_ID"
log "    scratch act: $SCRATCH_ACT_ID"
log "    tempdir:     $TMPDIR_ROOT (cleaned up on exit)"
exit 0
