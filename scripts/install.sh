#!/bin/sh
# ask installer — one-liner bootstrap for the agent-to-human request inbox.
#
# Usage (from a Claude Code user's terminal):
#   curl -fsSL https://raw.githubusercontent.com/aac/ask/main/scripts/install.sh | sh
#
# What it does:
#   1. Detects the host OS + arch via uname.
#   2. Downloads the matching tarball from the latest GitHub Release.
#   3. Extracts the `ask` binary and installs it on $PATH.
#   4. Prints follow-up steps (`ask install-skill`, `claude mcp add`).
#
# Install location:
#   - /usr/local/bin if writable, else with sudo if available;
#   - else $HOME/.local/bin (created if needed; warn if not on PATH).
#
# Env overrides (rarely needed):
#   ASK_VERSION   pin a specific tag, e.g. v1.1.0 (default: latest)
#   ASK_PREFIX    install dir override (skips the /usr/local/bin vs ~/.local/bin probe)
#   ASK_REPO      GitHub owner/name (default: aac/ask)

set -eu

REPO="${ASK_REPO:-aac/ask}"
VERSION="${ASK_VERSION:-latest}"
PREFIX="${ASK_PREFIX:-}"

# ---- helpers --------------------------------------------------------------

say()  { printf '%s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# ---- platform detection ---------------------------------------------------

os_raw="$(uname -s)"
arch_raw="$(uname -m)"

case "$os_raw" in
  Darwin)  os="darwin" ;;
  Linux)   os="linux" ;;
  *)       die "unsupported OS: $os_raw (supported: Darwin, Linux)" ;;
esac

case "$arch_raw" in
  x86_64|amd64)   arch="x86_64" ;;
  arm64|aarch64)  arch="arm64" ;;
  *)              die "unsupported arch: $arch_raw (supported: x86_64, arm64)" ;;
esac

# ---- pick a downloader ----------------------------------------------------

if have curl; then
  dl() { curl -fsSL "$1" -o "$2"; }
  dl_stdout() { curl -fsSL "$1"; }
elif have wget; then
  dl() { wget -qO "$2" "$1"; }
  dl_stdout() { wget -qO- "$1"; }
else
  die "need curl or wget on PATH"
fi

# ---- resolve version ------------------------------------------------------

if [ "$VERSION" = "latest" ]; then
  # Follow the GitHub Releases /latest redirect to extract the tag.
  # The releases/latest URL 302s to /releases/tag/vX.Y.Z; parse the trailing segment.
  if have curl; then
    resolved="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
      "https://github.com/${REPO}/releases/latest")"
  else
    # wget's --max-redirect 0 prints the Location; fall back to following.
    resolved="$(wget -q -S --max-redirect=0 \
      "https://github.com/${REPO}/releases/latest" 2>&1 \
      | awk '/^  Location: /{print $2; exit}')"
  fi
  VERSION="${resolved##*/}"
  case "$VERSION" in
    v*) ;;
    *)  die "could not resolve latest release for ${REPO}; set ASK_VERSION=vX.Y.Z explicitly" ;;
  esac
fi

# Strip leading v for the asset name (matches .goreleaser.yml `name_template`).
ver_bare="${VERSION#v}"

asset="ask_${ver_bare}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
sums_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

say "ask installer"
say "  repo:    ${REPO}"
say "  version: ${VERSION}"
say "  asset:   ${asset}"

# ---- decide install dir + sudo wrapper ------------------------------------

SUDO=""
if [ -n "$PREFIX" ]; then
  install_dir="$PREFIX"
elif [ -w "/usr/local/bin" ] 2>/dev/null; then
  install_dir="/usr/local/bin"
elif have sudo && [ -d "/usr/local/bin" ]; then
  install_dir="/usr/local/bin"
  SUDO="sudo"
else
  install_dir="${HOME}/.local/bin"
  mkdir -p "$install_dir"
fi

# ---- download + extract ---------------------------------------------------

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t ask-install)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say ""
say "downloading ${url}"
dl "$url" "$tmp/$asset"

# Checksum verification is best-effort: if the checksum file isn't published yet
# (early manual releases), skip with a warning rather than block install.
if dl "$sums_url" "$tmp/checksums.txt" 2>/dev/null; then
  expected="$(awk -v a="$asset" '$2 == a {print $1; exit}' "$tmp/checksums.txt" || true)"
  if [ -n "$expected" ]; then
    if have shasum; then
      actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
    elif have sha256sum; then
      actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
    else
      actual=""
      warn "no shasum/sha256sum on PATH; skipping checksum verify"
    fi
    if [ -n "$actual" ] && [ "$expected" != "$actual" ]; then
      die "checksum mismatch for $asset (expected $expected, got $actual)"
    fi
    [ -n "$actual" ] && say "checksum OK"
  else
    warn "no checksum entry for $asset in checksums.txt; skipping verify"
  fi
else
  warn "checksums.txt not published for ${VERSION}; skipping verify"
fi

say "extracting"
tar -xzf "$tmp/$asset" -C "$tmp"

if [ ! -f "$tmp/ask" ]; then
  die "tarball did not contain an 'ask' binary; aborting"
fi

# ---- install --------------------------------------------------------------

target="${install_dir}/ask"
say "installing to ${target}"
if [ -n "$SUDO" ]; then
  $SUDO install -m 0755 "$tmp/ask" "$target"
else
  install -m 0755 "$tmp/ask" "$target"
fi

# Drop the macOS quarantine attribute if present so Gatekeeper doesn't block the
# binary on first run. Silently no-op on Linux / when xattr is absent.
if [ "$os" = "darwin" ] && have xattr; then
  $SUDO xattr -d com.apple.quarantine "$target" 2>/dev/null || true
fi

# ---- PATH sanity check ----------------------------------------------------

case ":${PATH}:" in
  *":${install_dir}:"*) on_path=1 ;;
  *)                     on_path=0 ;;
esac

# ---- next-step hints ------------------------------------------------------

say ""
say "installed ask $("$target" version 2>/dev/null || echo "$VERSION") to ${target}"
say ""
say "next steps:"
say "  1. ask install-skill"
say "       Drops the Claude Code skill into ~/.claude/skills/ask/."
say ""
say "  2. claude mcp add --scope user ask -- ask mcp"
say "       Registers ask's MCP server with Claude Code (user scope = available in every repo)."
say ""
say "  3. Start a new Claude Code session (or /clear an existing one) so the new"
say "     skill is picked up. Then 'ask init' in any project to set up .ask/."
say ""

if [ "$on_path" = "0" ]; then
  warn "${install_dir} is not on your PATH."
  warn "Add this line to your shell rc (e.g. ~/.bashrc, ~/.zshrc):"
  warn "  export PATH=\"${install_dir}:\$PATH\""
fi
