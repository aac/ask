#!/usr/bin/env sh
# Ad-hoc code-sign a darwin Mach-O binary so it executes on Apple Silicon.
#
# Why: arm64 macOS refuses to run an unsigned binary (SIGKILL). The Go toolchain
# ad-hoc signs when you `go build` natively on a Mac, but GoReleaser cross-builds
# the darwin binaries on CI, leaving them unsigned. An ad-hoc signature (`-s -`)
# is free, needs no Apple Developer account, and is enough for the install paths
# we ship (plugin, `go install`, `curl | sh` — none of which quarantine). It does
# NOT clear the Gatekeeper "unidentified developer" prompt for browser downloads;
# that requires full notarization (a paid Apple account), which we don't do yet.
#
# Called from .goreleaser.yml as a build post-hook: codesign-adhoc.sh <os> <path>
# No-op for non-darwin builds. Requires `codesign`, so the release job runs on a
# macOS runner.
set -e
os="$1"
path="$2"
[ "$os" = "darwin" ] || exit 0
codesign --sign - --force --timestamp=none "$path"
echo "ad-hoc signed: $path"
