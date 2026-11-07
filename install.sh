#!/bin/sh
# Installs deck in Docker, or updates it: curl -fsSL https://raw.githubusercontent.com/teaarte/deck/master/install.sh | sh
# DECK_DIR is its folder (~/deck); DECK_SOURCE a folder of deck's files instead of GitHub, whose image is local too.
set -eu
dir=${DECK_DIR:-$HOME/deck}
src=${DECK_SOURCE:-https://raw.githubusercontent.com/teaarte/deck/master}
say() { echo "deck: $*"; }
command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 || {
  say "install Docker with compose first: OrbStack or Docker Desktop on a Mac, Docker Desktop on Windows, Docker Engine on Linux"
  exit 1
}
mkdir -p "$dir"
cd "$dir"
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
case $src in http*) docker compose pull -q deck ;; esac
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
