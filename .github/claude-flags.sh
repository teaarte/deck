#!/usr/bin/env bash
# claude-flags.sh checks that the claude on PATH, the version deck's image installs, takes every flag the fake Claude
# Code of the tests takes: a flag the CLI drops or renames fails here, not on the owner's deck. It runs claude in an
# empty home with no prompt, which fails on the options before anything is sent.
set -euo pipefail
fake=tests/harness/testdata/claude/fake.sh
cases=$(sed -n '/^  case "\$a" in$/,/^  esac$/p' "$fake")
plain=$(sed -n 's/^    \(-[^)]*\)) ;;$/\1/p' <<<"$cases" | head -1 | tr '|' ' ')
valued=$(sed -n 's/^    \(-[^)]*\)) ;;$/\1/p' <<<"$cases" | sed -n 2p | tr '|' ' ')
[ -n "$plain" ] && [ -n "$valued" ] || { echo "no flags found in $fake"; exit 1; }
home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
check() {
  out=$(env -i PATH="$PATH" HOME="$home" CLAUDE_CONFIG_DIR="$home" timeout 30 claude -p "$@" </dev/null 2>&1 || true)
  if grep -q "unknown option" <<<"$out"; then
    echo "claude $(claude --version) does not take $1, which the fake takes: $out"; exit 1
  fi
}
for f in $plain; do [ "$f" = -p ] || check "$f"; done
for f in $valued; do check "$f" x; done
echo "claude $(claude --version) takes every flag of the fake: $plain $valued"
