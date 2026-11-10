#!/bin/sh
# Installs deck in Docker, or updates it: curl -fsSL https://raw.githubusercontent.com/teaarte/deck/master/install.sh | sh
# DECK_DIR is its folder (~/deck); DECK_SOURCE a folder of deck's files instead of GitHub; DECK_YES=1 updates a running deck. Run from a checkout of deck
# (sh install.sh in it), it takes its master and runs the image deck:local built from it.
set -eu
dir=${DECK_DIR:-$HOME/deck}
here=$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)
case $0 in *install.sh) [ -n "${DECK_SOURCE:-}" ] || [ ! -f "$here/Dockerfile" ] || [ ! -f "$here/compose.yaml" ] || DECK_SOURCE=$here ;; esac
src=${DECK_SOURCE:-https://raw.githubusercontent.com/teaarte/deck/master}
say() { echo "deck: $*"; }
command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 || {
  say "install Docker with compose first: OrbStack or Docker Desktop on a Mac, Docker Desktop on Windows, Docker Engine on Linux"
  exit 1
}
mkdir -p "$dir"
cd "$dir"
# A deck that runs here is replaced only by a yes in a terminal, by DECK_YES=1, or when DECK_DIR names this folder.
if [ -z "${DECK_DIR:-}" ] && [ -z "${DECK_YES:-}" ] && [ -f compose.yaml ] && [ -n "$(docker compose ps -q deck 2>/dev/null)" ]; then
  if (: </dev/tty) 2>/dev/null; then
    printf 'deck: a deck runs in %s; update and restart it? [y/N] ' "$dir" >/dev/tty
    read -r answer </dev/tty || answer=
    case $answer in y | Y | yes | да | Да) ;; *) say "left as it was" && exit 1 ;; esac
  else
    say "a deck runs in $dir and there is no terminal to ask; run with DECK_YES=1 to update it, or DECK_DIR=<folder> for another"
    exit 1
  fi
fi
for f in compose.yaml seccomp.json deck-box.apparmor move-to-docker.sh; do
  case $src in
  http*) curl -fsSL -o "$f" "$src/$f" ;;
  *) cp "$src/$f" "$f" ;;
  esac
done
# Ubuntu and Debian run the container under the AppArmor profile deck-box, which the host loads once.
if command -v apparmor_parser >/dev/null 2>&1 && ! cmp -s deck-box.apparmor /etc/apparmor.d/deck-box; then
  as=""
  [ "$(id -u)" = 0 ] || as=sudo
  say "loading the AppArmor profile deck-box${as:+ (sudo asks for your password)}"
  $as install -m644 deck-box.apparmor /etc/apparmor.d/deck-box
  $as apparmor_parser -r /etc/apparmor.d/deck-box
fi
touch .env
case $src in
http*)
  if grep -qs 'image: deck:local' compose.override.yaml; then
    say "compose.override.yaml runs deck:local, the image of a checkout: remove its line image to run the published one"
  fi
  docker compose pull -q deck
  ;;
*)
  if [ -f "$src/Dockerfile" ]; then
    if [ "$(git -C "$src" branch --show-current 2>/dev/null)" = master ]; then
      say "taking master of $src"
      git -C "$src" pull -q --ff-only
    fi
    say "building the image deck:local of $src"
    # git names the commit of a worktree too, whose .git is a file the build does not see.
    commit=$(git -C "$src" rev-parse --short=12 HEAD 2>/dev/null || true)
    if [ -n "$commit" ] && ! git -C "$src" diff --quiet HEAD 2>/dev/null; then commit=$commit-dirty; fi
    docker build -q --build-arg COMMIT="$commit" -t deck:local "$src" >/dev/null
    # The override names the image; whatever else it says stays.
    if [ ! -f compose.override.yaml ]; then
      printf 'services:\n  deck:\n    image: deck:local\n' > compose.override.yaml
    elif ! grep -qs 'image: deck:local' compose.override.yaml; then
      if grep -qs '^    image:' compose.override.yaml; then
        sed -i.bak 's|^    image:.*|    image: deck:local|' compose.override.yaml
      elif grep -qs '^  deck:' compose.override.yaml; then
        sed -i.bak 's|^  deck:.*|&\
    image: deck:local|' compose.override.yaml
      else
        say "add under services: deck: the line image: deck:local to compose.override.yaml, then run this again"
        exit 1
      fi
      rm -f compose.override.yaml.bak
    fi
  fi
  ;;
esac
docker compose up -d
say "waiting for deck"
i=0
until docker compose exec -T deck curl -fso /dev/null http://127.0.0.1:7700/ 2>/dev/null; do
  i=$((i + 1))
  if [ $i -ge 60 ]; then
    say "deck did not start in two minutes; see: cd $dir && docker compose logs deck"
    exit 1
  fi
  sleep 2
done
say "deck runs at http://deck.localhost:7700 on this machine; this link pairs a device:"
docker compose exec -T deck deck pair
say "another link later: cd $dir && docker compose exec deck deck pair"
