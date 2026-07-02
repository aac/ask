#!/bin/sh
# ask installer — build binary, install skill, register MCP server.
#
# Usage:
#   ./install.sh                           # auto-detect harness
#   ./install.sh --target claude           # install for Claude Code
#   ./install.sh --target codex            # install for Codex
#   ./install.sh --prefix ~/.local         # binary destination prefix
#   ./install.sh --uninstall               # remove binary + MCP entry
#
# What it does:
#   1. Detects the target harness (claude or codex), or accepts --target flag.
#   2. Verifies Go is installed.
#   3. Builds ask from source into $PREFIX/bin/ask (default ~/.local/bin).
#   4. Copies skills/ask/ from the checkout into ~/.claude/skills/ask (or codex equiv).
#   5. Registers the ask MCP server in the agent's config:
#        Claude: claude mcp add --scope user ask -- ask mcp
#        Codex : codex mcp add ask -- ask mcp
#   6. Verifies the binary and prints confirmation.
#
# Idempotent — safe to re-run. Existing binary is replaced. MCP registration
# is skipped if the entry already exists with the same command.
#
# Safety / curl-pipe posture:
#   Script prints every action before taking it. MCP registration requires
#   --yes when overwriting an existing entry that differs; new entries are
#   always added silently. Binary builds and skill installs never prompt.
#
# Uninstall:
#   --uninstall removes $PREFIX/bin/ask and the MCP server entry. Skills
#   directory (~/.{claude,codex}/skills/ask) is NOT removed — it may contain
#   user edits. Remove manually if desired.
#
# Env overrides:
#   ASK_REPO_DIR  Path to the ask repo (default: directory containing this script)

set -eu

REPO_DIR="${ASK_REPO_DIR:-$(cd "$(dirname "$0")" && pwd)}"

# ---- helpers ----------------------------------------------------------------

say()  { printf '%s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# ---- argument parsing -------------------------------------------------------

TARGET=""
PREFIX="${HOME}/.local"
UNINSTALL=false
YES=false

while [ $# -gt 0 ]; do
  case "$1" in
    --target)
      [ $# -ge 2 ] || die "--target requires a value (claude or codex)"
      TARGET="$2"
      shift 2
      ;;
    --target=*)
      TARGET="${1#--target=}"
      shift
      ;;
    --prefix)
      [ $# -ge 2 ] || die "--prefix requires a value"
      PREFIX="$2"
      shift 2
      ;;
    --prefix=*)
      PREFIX="${1#--prefix=}"
      shift
      ;;
    --uninstall)
      UNINSTALL=true
      shift
      ;;
    --yes|-y)
      YES=true
      shift
      ;;
    -h|--help)
      say "Usage: $0 [--target claude|codex] [--prefix DIR] [--uninstall] [--yes]"
      say ""
      say "Builds ask from source and registers it as an MCP server in your agent harness."
      say ""
      say "Options:"
      say "  --target claude|codex   Skip auto-detection; install for the given harness."
      say "  --prefix DIR            Binary install prefix (default: ~/.local)."
      say "                          Binary is placed at PREFIX/bin/ask."
      say "  --uninstall             Remove the binary and MCP registration."
      say "  --yes, -y               Auto-confirm overwriting an existing MCP entry that"
      say "                          differs. Required when running non-interactively (e.g."
      say "                          curl … | sh) if an entry already exists."
      say "  -h, --help              Show this help."
      say ""
      say "Environment:"
      say "  ASK_REPO_DIR  Path to ask repo (default: directory containing this script)"
      exit 0
      ;;
    *)
      die "unknown argument: $1 (try --help)"
      ;;
  esac
done

# ---- validate target --------------------------------------------------------

if [ -n "$TARGET" ]; then
  case "$TARGET" in
    claude|codex) ;;
    *) die "invalid target: ${TARGET} (must be claude or codex)" ;;
  esac
fi

# ---- harness detection ------------------------------------------------------

detect_harness() {
  if [ -d "${HOME}/.claude" ]; then
    echo "claude"
  elif [ -d "${HOME}/.codex" ]; then
    echo "codex"
  else
    echo ""
  fi
}

if [ -z "$TARGET" ]; then
  TARGET="$(detect_harness)"
  if [ -z "$TARGET" ]; then
    die "could not detect harness (no ~/.claude or ~/.codex found). Use --target claude or --target codex."
  fi
  say "detected harness: ${TARGET}"
else
  say "target harness: ${TARGET}"
fi

BIN_DIR="${PREFIX}/bin"
BIN_PATH="${BIN_DIR}/ask"

# ---- uninstall path ---------------------------------------------------------

if $UNINSTALL; then
  say "uninstalling ask for ${TARGET}..."

  if [ -f "$BIN_PATH" ]; then
    say "  removing binary: ${BIN_PATH}"
    rm "$BIN_PATH"
  else
    say "  binary not found at ${BIN_PATH}; skipping"
  fi

  case "$TARGET" in
    claude)
      if claude mcp get ask >/dev/null 2>&1; then
        say "  removing Claude MCP entry: ask"
        claude mcp remove ask -s user 2>/dev/null || warn "claude mcp remove failed"
      else
        say "  no Claude MCP entry found; skipping"
      fi
      ;;
    codex)
      if codex mcp get ask >/dev/null 2>&1; then
        say "  removing Codex MCP entry: ask"
        codex mcp remove ask 2>/dev/null || warn "codex mcp remove failed"
      else
        say "  no Codex MCP entry found; skipping"
      fi
      ;;
  esac

  say ""
  say "ask uninstalled from ${TARGET}."
  exit 0
fi

# ---- verify repo ------------------------------------------------------------

[ -f "${REPO_DIR}/go.mod" ] || die "go.mod not found in ${REPO_DIR}; run from the ask repo or set ASK_REPO_DIR"
[ -d "${REPO_DIR}/cmd/ask" ] || die "cmd/ask not found in ${REPO_DIR}"

# ---- verify Go --------------------------------------------------------------

have go || die "go required to build ask; install from https://go.dev/dl/ or wait for a release-download fallback"
say "using Go: $(go version)"

# ---- build binary -----------------------------------------------------------

say "building ask..."
mkdir -p "$BIN_DIR"
(cd "$REPO_DIR" && go build -o "$BIN_PATH" ./cmd/ask) || die "go build failed"
say "binary: ${BIN_PATH}"

# ---- install skill ----------------------------------------------------------
# The skill ships as plain files in the checkout (skills/ask/); copy them into
# the harness skills dir. No longer embedded in the binary — the plugin install
# is the canonical skill path, and this source install just copies from the repo.

case "$TARGET" in
  claude) SKILL_DEST="${HOME}/.claude/skills/ask" ;;
  codex)  SKILL_DEST="${HOME}/.codex/skills/ask" ;;
  *)      SKILL_DEST="" ;;
esac
if [ -n "$SKILL_DEST" ] && [ -d "${REPO_DIR}/skills/ask" ]; then
  say "installing ask skill to ${SKILL_DEST}..."
  mkdir -p "$SKILL_DEST"
  cp -R "${REPO_DIR}/skills/ask/." "$SKILL_DEST/" || warn "skill copy failed (non-fatal; copy ${REPO_DIR}/skills/ask manually)"
else
  warn "skill not installed (skills/ask not found in ${REPO_DIR}); copy it manually if needed"
fi

# ---- register MCP server ----------------------------------------------------

register_claude() {
  if claude mcp get ask >/dev/null 2>&1; then
    say "Claude MCP entry for 'ask' already exists; verifying..."
    existing_cmd="$(claude mcp get ask 2>/dev/null | grep 'Command:' | sed 's/.*Command: *//')"
    existing_args="$(claude mcp get ask 2>/dev/null | grep 'Args:' | sed 's/.*Args: *//')"
    if [ "$existing_cmd" = "ask" ] && [ "$existing_args" = "mcp" ]; then
      say "  entry is correct; no update needed."
    else
      say "  existing entry differs:"
      say "    command: ${existing_cmd:-<none>} (want: ask)"
      say "    args:    ${existing_args:-<none>} (want: mcp)"
      if $YES; then
        say "  replacing (--yes)..."
        claude mcp remove ask -s user 2>/dev/null || true
        claude mcp add --scope user ask -- ask mcp
        say "  replaced Claude MCP entry."
      else
        die "existing MCP entry differs; re-run with --yes to overwrite, or run: claude mcp remove ask -s user"
      fi
    fi
  else
    say "registering ask MCP server with Claude Code (user scope)..."
    claude mcp add --scope user ask -- ask mcp
    say "registered."
  fi
}

register_codex() {
  if codex mcp get ask >/dev/null 2>&1; then
    say "Codex MCP entry for 'ask' already exists; no update needed."
  else
    say "registering ask MCP server with Codex..."
    codex mcp add ask -- ask mcp
    say "registered."
  fi
}

case "$TARGET" in
  claude) have claude || die "claude binary not found; is Claude Code installed?"; register_claude ;;
  codex)  have codex  || die "codex binary not found; is Codex installed?"; register_codex ;;
esac

# ---- verify -----------------------------------------------------------------

say ""
"$BIN_PATH" version 2>/dev/null || "$BIN_PATH" --help 2>/dev/null | head -3 || warn "could not verify binary (check ${BIN_PATH})"

say ""
say "ask installed for ${TARGET}."
say "  binary: ${BIN_PATH}"
say ""
say "Start a new session (or /clear) so the skill and MCP server are picked up."
