#!/bin/sh
# Moves the folder of a deck that ran without Docker into the volume of deck's compose.yaml beside this script; run
# again, it brings the folder over anew. Usage: sh move-to-docker.sh [folder, ~/.deck2 by default]
set -eu
from=${1:-$HOME/.deck2}
[ -f "$from/state/deck.db" ] || { echo "move-to-docker: $from holds no deck" >&2; exit 1; }
if lsof "$from/state/deck.db" >/dev/null 2>&1; then
  echo "move-to-docker: a deck still runs on $from; stop it first" >&2
  exit 1
fi
from=$(cd "$from" && pwd -P)
cd "$(dirname "$0")"
[ -f compose.yaml ] || { echo "move-to-docker: put it beside deck's compose.yaml" >&2; exit 1; }
docker compose stop deck >/dev/null 2>&1 || true
docker compose create deck
# The volume gets the folder whole: the database, files, work, secrets, keys, devices, the homes of the CLIs and the
# tailnet's state, so deck keeps its address there; what only a running deck holds stays behind.
docker compose run --rm --no-deps -T --entrypoint sh -v "$from:/from:ro" deck -c '
  set -eu
  find /var/lib/deck -mindepth 1 -delete
  cd /from
  tar -cf - --exclude=./state/deck.lock --exclude=./state/doors --exclude=.DS_Store . | tar -xf - -C /var/lib/deck
  chown -R deck:deck /var/lib/deck'
if [ -d "$from/state/tailscale" ] && ! grep -qs '^DECK_TAILSCALE=.' .env; then
  echo "move-to-docker: deck was on a tailnet; put its name in .env (DECK_TAILSCALE=deck) to keep its address there"
fi
echo "move-to-docker: $from is in the volume; docker compose up -d starts deck on it, and $from stays as it was"
