#!/bin/sh
# Fake Claude Code CLI: rejects unknown flags, records how deck started it, runs before, then replays stream.<n> or stream.
d=$(dirname "$0")
if [ "$1" = "--version" ]; then
  echo "$(cat "$d/version" 2>/dev/null || echo 2.1.281) (Claude Code)"
  exit 0
fi
mkdir -p "$d/calls"
n=$(( $(ls "$d/calls" | wc -l) + 1 ))
c="$d/calls/$n"
mkdir "$c"
pwd > "$c/cwd"
env > "$c/env"
: > "$c/args"
session=""
prev=""
for a in "$@"; do
  printf '%s\n' "$a" >> "$c/args"
  case "$prev" in
    --session-id|--resume) session="$a" ;;
    --mcp-config) ls -l "$a" | cut -c1-10 > "$c/mcpmode"; cp "$a" "$c/mcpconfig" ;;
    --settings) ls -l "$a" | cut -c1-10 > "$c/settingsmode"; cp "$a" "$c/settings" ;;
    --plugin-dir) cp -R "$a" "$c/plugin" ;;
  esac
  case "$prev" in
    --input-format|--output-format|--mcp-config|--settings|--disallowedTools|--setting-sources|--permission-prompt-tool|--allowedTools|--resume|--session-id|--plugin-dir) prev="" ; continue ;;
  esac
  case "$a" in
    -p|--verbose|--strict-mcp-config) ;;
    --input-format|--output-format|--mcp-config|--settings|--disallowedTools|--setting-sources|--permission-prompt-tool|--allowedTools|--resume|--session-id|--plugin-dir) ;;
    *) echo "error: unknown option '$a'" >&2; exit 1 ;;
  esac
  prev="$a"
done
[ -f "$d/nostdin" ] || cat > "$c/stdin"
word=$(grep -o 'deck-check-[0-9a-f]*' "$c/stdin" | head -1)
if [ -n "$word" ]; then echo "$word" > "$d/word"; else word=$(cat "$d/word" 2>/dev/null); fi
[ -f "$d/before" ] && . "$d/before"
stream="$d/stream.$n"
[ -f "$stream" ] || stream="$d/stream"
[ -f "$stream" ] && sed "s/{{session}}/$session/g; s/{{word}}/$word/g" "$stream"
[ -f "$d/after" ] && . "$d/after"
exit 0
