#!/bin/sh
# Fake Claude Code CLI: rejects unknown flags, records how deck started it, runs before, then replays stream.<n>,
# stream.<model> or stream. A start without a session lists the models: it prints models, uncounted.
d=$(dirname "$0")
if [ "$1" = "--version" ]; then
  echo "$(cat "$d/version" 2>/dev/null || echo 2.1.281) (Claude Code)"
  exit 0
fi
if [ "$1" = "auth" ] && [ "$2" = "login" ]; then
  echo "Opening browser to sign in..."
  echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=fake"
  printf 'Paste code here if prompted > '
  read -r code
  [ "$code" = "good-code" ] || { echo "OAuth error: invalid code" >&2; exit 1; }
  echo "$code" > "$d/signed-in"
  echo "Login successful."
  exit 0
fi
case " $* " in
  *" --session-id "*|*" --resume "*) ;;
  *" --input-format "*) cat "$d/models" 2>/dev/null; exit 0 ;;
esac
mkdir -p "$d/calls"
n=$(( $(ls "$d/calls" | wc -l) + 1 ))
c="$d/calls/$n"
mkdir "$c"
pwd > "$c/cwd"
env > "$c/env"
: > "$c/args"
session=""
model=""
stream=""
prev=""
for a in "$@"; do
  printf '%s\n' "$a" >> "$c/args"
  case "$prev" in
    --session-id|--resume) session="$a" ;;
    --model) model="$a" ;;
    --mcp-config) ls -l "$a" | cut -c1-10 > "$c/mcpmode"; cp "$a" "$c/mcpconfig" ;;
    --settings) ls -l "$a" | cut -c1-10 > "$c/settingsmode"; cp "$a" "$c/settings" ;;
    --plugin-dir) cp -R "$a" "$c/plugin" ;;
  esac
  case "$prev" in
    --input-format|--output-format|--mcp-config|--settings|--disallowedTools|--setting-sources|--permission-prompt-tool|--allowedTools|--resume|--session-id|--plugin-dir|--permission-mode|--add-dir|--tools|--model|--effort) prev="" ; continue ;;
  esac
  case "$a" in
    -p|--verbose|--strict-mcp-config) ;;
    --input-format|--output-format|--mcp-config|--settings|--disallowedTools|--setting-sources|--permission-prompt-tool|--allowedTools|--resume|--session-id|--plugin-dir|--permission-mode|--add-dir|--tools|--model|--effort) ;;
    *) echo "error: unknown option '$a'" >&2; exit 1 ;;
  esac
  prev="$a"
done
# Like the CLI, it reads stdin up to the first user message, and the rest, such as live messages, as it comes.
if [ ! -f "$d/nostdin" ]; then
  : > "$c/stdin"
  while IFS= read -r line; do
    printf '%s\n' "$line" >> "$c/stdin"
    case "$line" in *'"type":"user"'*) break ;; esac
  done
  exec 3<&0
  cat >> "$c/stdin" <&3 &
fi
word=$(grep -o 'deck-check-[0-9a-f]*' "$c/stdin" | head -1)
if [ -n "$word" ]; then echo "$word" > "$d/word"; else word=$(cat "$d/word" 2>/dev/null); fi
export DECK_FAKE_FAILED="$d/failed"
[ -f "$d/before" ] && . "$d/before"
[ -n "$stream" ] || stream="$d/stream.$n"
[ -f "$stream" ] || stream="$d/stream.$model"
[ -f "$stream" ] || stream="$d/stream"
[ -f "$stream" ] && sed "s/{{session}}/$session/g; s/{{word}}/$word/g" "$stream"
[ -f "$d/after" ] && . "$d/after"
exit 0
