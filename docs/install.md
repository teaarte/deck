# Installing deck: the details

The short way is in the [README](../README.md#get-started). This page has the rest: what you need first, install by
hand, first steps, signing in, the phone and other devices, a server with a domain, what the container needs, behind
the host's tailscale serve, the search beside deck, a model server beside deck, Windows, updating, moving a deck that
ran without Docker, moving off the built-in Tailscale, and deck without Docker.

## What you need first

One way for every machine: Docker with compose and deck's image, with Claude Code, Codex, uv and a browser inside.

- **Linux:** [Docker Engine](https://docs.docker.com/engine/install/) with the compose plugin, your user in the group
  `docker` ([how](https://docs.docker.com/engine/install/linux-postinstall/)).
- **Mac:** [OrbStack](https://orbstack.dev) or Docker Desktop.
- **Windows:** Docker Desktop with WSL 2 (not tried yet, see [Windows](#windows)).

Run the install line as your user and not with `sudo` (under `sudo` it would put deck in root's home, `/root/deck`):

```bash
curl -fsSL https://raw.githubusercontent.com/teaarte/deck/master/install.sh | sh
```

It puts deck in `~/deck` (`DECK_DIR` for another folder), loads the AppArmor profile on Ubuntu and Debian (`sudo` asks
once, for that alone), starts deck and prints a link that pairs your device (on a fresh server, write `.env` first:
[on a server](#on-a-server)). deck restarts with Docker and the machine; its data and the agents' logins stay in the
volume `deck`. Its settings are the file `.env` beside `compose.yaml` (in `~/deck`); the installer keeps an existing one.

**Memory and processes.** The image takes about 2.3 GB of disk; deck holds a few hundred MB of memory, each agent at
work and the browser more. The container runs at most 4096 processes; its memory has a ceiling only by
`DECK_MEM_LIMIT=6g` in `.env`, past which a process in deck's container is killed (deck itself maybe, and Docker starts
it again), not the host's other services. A deck that stops answering shows as `unhealthy` in `docker compose ps`.

**What it holds, what you add.** The image carries deck and the programs its work runs: the CLIs of Claude Code, Codex
and goose, Python with uv, Node, ffmpeg, git and Chromium. Beside it in `compose.yaml` runs
[SearXNG](https://docs.searxng.org), the web search of the agents ([below](#the-search-beside-deck)). Models are not in
it and never live in deck: you add one way to reach them in Settings → Models — sign in to a Claude or ChatGPT
subscription, or give an OpenRouter key, or a free key of Groq or NVIDIA — and Deki proposes which model takes each
kind of work. A model of your own hardware is a ready open-source server ([below](#a-model-server-beside-deck)); a
server on your Mac comes through a [node](node.md).

## Install by hand

```bash
mkdir deck && cd deck
v=$(curl -fsSLo /dev/null -w '%{url_effective}' https://github.com/teaarte/deck/releases/latest); v=${v##*/}   # the latest release
for f in compose.yaml seccomp.json deck-box.apparmor move-to-docker.sh; do curl -fsSLO https://raw.githubusercontent.com/teaarte/deck/$v/$f; done
# Ubuntu and Debian, once: the AppArmor profile the container runs under
sudo install -m644 deck-box.apparmor /etc/apparmor.d/deck-box && sudo apparmor_parser -r /etc/apparmor.d/deck-box
docker compose up -d
docker compose exec deck deck pair        # a link that pairs a device
```

## First steps

Open the link, then sign in to the agents in Settings → Models → **Sign in** (Codex shows a link and a code, Claude
Code a link whose page gives the code), and set your time zone in Settings → Advanced → Limits: schedules and the day
of Today follow it. An empty deck says "Tell Deki what to do": describe a routine in the chat and Deki builds the
module, or add the starter module it offers as a draft. The models, agents and folders of your own computer reach deck
in the container through a [node](node.md): the deck binary on your Mac, outside Docker, that lends deck its local
models and folders.

## Signing in

There is no password and no owner token. A device pairs once by a link of `deck pair` (or of Settings → Devices on a
device already signed in, with a QR code for a phone) and makes a passkey; the link works once, within ten minutes.
Settings → Devices lists every device and passkey: revoking a device signs out that browser alone, revoking a passkey
every browser that used it. An answer that trusts for long — always to a right that reaches far (a login, money,
sending, other modules, the whole web, an agent's shell, others' data), enabling a draft, a new token, a link that
pairs a device, [«I checked it: trust»](architecture.md#rights-requests-and-trust) of a module's data or a token —
takes a touch of the passkey within five minutes; once, no and a revoke take none. An automation token calls the
commands of deck it lists, in the modules its rights name.

## The phone and other devices

**On this machine** deck is <http://deck.localhost:7700>: browsers treat `deck.localhost` as secure, so a passkey works
there without https (tried in Chromium and Safari). That address opens only on the machine deck runs on.

### On a server

Give deck its https address before you pair: add it to `~/deck/.env`, then run the install line (it keeps `.env`) or,
if deck runs already, `docker compose up -d` in `~/deck`; `docker compose exec deck deck pair` prints the https link
first. With a domain, see [a server with a domain](#a-server-with-a-domain); without one, Tailscale gives the address,
below.

### From a phone, through Tailscale

From a phone and other devices deck needs https: your domain, or Tailscale on deck's machine and on the device.

1. Install Tailscale on the phone and on deck's machine and sign both in to one tailnet; on deck's machine: on a Mac
   the Tailscale app with its command line (the app's Settings → Install CLI), on a Linux server
   `curl -fsSL https://tailscale.com/install.sh | sh && sudo tailscale up --operator=$USER`.
2. On that machine: `tailscale serve --bg 7700` and `tailscale serve --bg --https=8443 7701` (if it asks, enable HTTPS
   in the Tailscale admin console); `tailscale serve status` shows the address, `https://<machine>.<tailnet>.ts.net`.
3. Put `DECK_DOMAIN=<machine>.<tailnet>.ts.net` in `~/deck/.env` (no `COMPOSE_PROFILES=domain`: Tailscale serves
   https, not Caddy) and run `docker compose up -d`. deck counts the sign-in steps of every client of `tailscale serve`
   as one address ([below](#behind-the-hosts-tailscale-serve)).
4. `docker compose exec deck deck pair` and open the https link on the phone; it makes a passkey there. Add deck to the
   phone's home screen: pushes and sharing a link or a file into a module work as in a native app.

## A server with a domain

On a Linux server (amd64 or arm64) with a domain name whose A/AAAA record points at it and ports 80, 443 and 8443
open in its firewall, add two lines to
`.env` beside `compose.yaml` (`~/deck/.env`); Caddy in the same compose then gets the HTTPS certificate and serves deck
at that name, with no Tailscale:

```bash
cd ~/deck
printf 'DECK_DOMAIN=deck.example.com\nCOMPOSE_PROFILES=domain\n' >> .env
docker compose up -d
docker compose exec deck deck pair     # the https link first; the deck.localhost one opens only on the server
```

The install line keeps an existing `.env`, so these lines may also come before it, and the link it prints is then the
https one first. Without `COMPOSE_PROFILES=domain` Caddy does not start (deck says so in its log).

## What the container needs

Every program of a module runs in a box of its own inside the container, built by bubblewrap as on any Linux. For that
`compose.yaml` gives the container three lines, and nothing else is loosened:

- `seccomp=./seccomp.json` — Docker's default profile with `clone`, `unshare`, `mount`, `umount2` and `pivot_root`
  allowed;
- `systempaths=unconfined` — so a box mounts its own `/proc`;
- `apparmor=deck-box` — Docker's `docker-default` with `mount` and `pivot_root` allowed. A host without AppArmor
  ignores it; on a host with AppArmor where the profile is not loaded, Docker does not start the container and says
  the profile is not found.

Without these lines deck starts no foreign program, and Settings → Health says which line is missing. Tried on Ubuntu
22.04 and 24.04 with Docker, rootless Docker and OrbStack; Docker Desktop and SELinux hosts (Fedora, RHEL) are not
tried yet.

## Behind the host's tailscale serve

deck counts the steps of signing in of each client by its address, and takes X-Forwarded-For only from Caddy of
`compose.yaml` (`DECK_PROXY: caddy`). Behind `tailscale serve` of the host every client reaches the container from the
gateway of its docker network, so deck counts them all as one address: thirty steps of signing in a minute together.
The gateway is no proxy of deck, as any container of the host comes from it too; a `.env` or compose of before that
names it (`DECK_PROXY=caddy,gateway`) works, and Settings → Health says to drop `gateway`. Without Docker deck takes
X-Forwarded-For from no address, loopback too, unless `-proxy` names it (`-proxy 127.0.0.1` behind the host's own
proxy).

## The search beside deck

`compose.yaml` runs [SearXNG](https://docs.searxng.org) as the service `searxng`, with its settings (JSON answers, no
limiter, English results) as a config of the same file and no port of its own: only deck reaches it. It takes about
270 MB of disk; when its engines do not answer, a search goes to the next one you assign. The first run
gives deck the connection `searxng` (`http://searxng:8080`), its model of kind search and the assignment of search,
unless a search, or a connection or model named `searxng`, was set up before; then `deck.web_search` of the agents works with nothing to set up. Your own search
stays yours: change the assignment in Settings → Models; to go without SearXNG, remove its assignment there and the
service in `compose.override.yaml` (`services: {searxng: {profiles: [off]}}`). deck without Docker has no such service: ask Deki to set SearXNG up (its
models skill), or point the connection `searxng` at one you run.

## A model server beside deck

deck runs no model itself: a model of your own hardware is a ready open-source server (Ollama, llama.cpp, whisper.cpp,
LocalAI). On deck's machine add it as a service to `compose.override.yaml` beside `compose.yaml` (an update downloads
`compose.yaml` again; the override stays, and the one `sh install.sh` of a checkout wrote already holds `services:`), such as

```yaml
services:
  ollama:
    image: ollama/ollama
    volumes: [ollama:/root/.ollama]
volumes:
  ollama:
```

then `docker compose up -d` and, in Settings → Models → Connections, a connection of the address by the service name,
`http://ollama:11434/v1`, marked local; its models keep their weights in the server's own volume, not in deck's. A
server on another machine comes through a [node](node.md) or by its address on the local network.

## Windows

Not tried yet: GitHub's Windows runners do not run Linux containers, so no test covers it. Install Docker Desktop
with the WSL 2 backend, download the files of [install by hand](#install-by-hand) into a folder (`curl.exe` in PowerShell, or
in WSL) and run `docker compose up -d` there, skipping the AppArmor step. deck works when Settings → Health shows the
box `ok`, a script of a starter module runs, and the link of `docker compose exec deck deck pair` pairs at
http://deck.localhost:7700 in Edge or Chrome.

## Moving from deck without Docker

A deck that ran as a binary (`~/.deck2`) moves into the volume whole: its data, devices and passkeys, secrets, the
homes of the CLIs, and its tailnet's state.

1. Stop that deck and keep a copy of its folder: `cp -a ~/.deck2 ~/.deck2-backup-$(date +%F)`.
2. Beside `compose.yaml`, run `sh move-to-docker.sh ~/.deck2` (with `DECK_TAILSCALE=<its name on the tailnet>` in
   `.env` if it was on one, until you move off the built-in Tailscale). Run again, it brings the folder over anew.
3. `docker compose up -d`, then sign in at <http://deck.localhost:7700> with the same devices.
4. deck in the container sees no folder and no model of the machine: pair a [node](node.md) on it, set **Node** of the
   connections at `127.0.0.1`, and in each module's settings, Access, remove the folders of deck's machine and add them
   again on the node. The CLIs of the image sign in once in Settings → Models, or the node's agents take the turns
   (`claude-code@Mac`).

To go back, `docker compose down` and start the old binary on `~/.deck2`, which the move leaves as it was.

## Updating

Run the install line again: it pulls the image, downloads `compose.yaml`, `seccomp.json`, `deck-box.apparmor` and
`move-to-docker.sh` of the release that image is and restarts deck (it asks before replacing a running deck; without a
terminal, `DECK_YES=1`). `docker compose pull && docker compose up -d` in `~/deck` updates only the image. When a release
is out, Settings → Advanced and the feed say so. On start, deck migrates its database and the stored manifests, keeping
the base before in `state/backups/update-<time>.tar.gz`; an older image refuses to start on the migrated base until that
snapshot is back ([restoring a snapshot](#restoring-a-snapshot)).

### Updating from an older release

Every release from v0.2.0 on updates to the current one with its data: modules and their records, secrets, documents,
the logins of the CLIs and paired devices (a test updates each released image from v0.2.0 and the v0.3.2 binary).
Before updating, keep a copy of the volume or a snapshot (Settings → Advanced): an older deck is not tested on a
database a newer one migrated. The floor rises only by a decision of its own, not with every minor release.

- **From v0.1.** A database of v0.1 is refused at start, before anything touches it, with the advice in the log.
  Update to v0.3.2 first, as the README of v0.3.2 says (its image copies the logins of v0.1's volume `agents` into
  deck's), start it once, then update on. A binary of v0.1 updates to v0.3.2 by hand first.
- **The image, from v0.2.** Update with the install line, which downloads the new `compose.yaml`: after v0.3.1 it runs
  SearXNG beside deck, the search the update assigns. `docker compose pull` alone leaves it out, and the health of
  `model:searxng` says so at the first search.
- **The image `deck:local`, built from a checkout.** `sh install.sh` in the checkout: it pulls master, builds
  `deck:local` and restarts deck with it (Settings → Health, the row `update`, says the same).
- **A released binary.** Settings → Advanced → **Update**: deck checks the signed `release.json`, takes the same build
  (`-tailnet` stays `-tailnet`) and restarts ([without Docker](#without-docker)).
- **The former deck.app or the installer without Docker.** By hand: stop it, keep a copy of `~/.deck2`, install as
  above and move its folder into the volume with `sh move-to-docker.sh ~/.deck2`
  ([moving from deck without Docker](#moving-from-deck-without-docker)).

## Restoring a snapshot

deck keeps its snapshots in its volume, `state/backups/`: `deck-<time>.tar.gz` daily and by hand, `deleted-<time>.tar.gz`
before a module is deleted, and `update-<time>.tar.gz`, the base as it was before a newer deck migrated it on its
first start. An older deck (v0.3.1 and later) refuses to start on a base a newer one migrated
and says so in `docker compose logs deck`; v0.3.0 and earlier do not check, so restore before going back to them. To go back to the older image, put back the base of the last `update-` snapshot:

```bash
cd ~/deck && docker compose stop deck
docker compose run --rm --no-deps --user deck --entrypoint ls deck /var/lib/deck/state/backups   # the newest update-<time>.tar.gz
docker compose run --rm --no-deps --user deck --entrypoint sh deck -c 'cd /var/lib/deck &&
  rm -f state/deck.db state/deck.db-wal state/deck.db-shm && tar -xzf state/backups/update-<time>.tar.gz state/deck.db'
```

Then name the older image in `compose.override.yaml` (create it if there is none), under `services:` → `deck:` the
line `image: ghcr.io/teaarte/deck:<older version>`, and `docker compose up -d`. The files are deck's as the user
`deck` extracts them; as root they would be root's and deck could not open its base.

What deck wrote to its base after that update is gone; stored files and data folders stay as they are. A daily
snapshot goes back the same way, with its `state/files` and `work/projects/*/data` too (`tar -xzf <snapshot>` in
`/var/lib/deck`; data files past the snapshot's budget, 2 GB by default, are not in it). Without Docker the same steps run in `~/.deck2` with deck stopped. Back on the newest image, remove
that line `image` from `compose.override.yaml` before the install line runs again.

A deck that updates itself as a binary (`deck.update_install`, without Docker) goes back by itself: when the new
binary migrated the base and fell before it served, the next start puts the base of that update's own `update-`
snapshot back, removes `deck.db-wal` and `deck.db-shm` and runs the binary before the update. What the new deck wrote
between its migration and its fall is gone. The steps above are for an image, or for a base whose snapshot was removed.

## Moving off the built-in Tailscale

`DECK_TAILSCALE=deck` (deck joins your tailnet itself) goes away in a coming release; deck warns in its log and in
Settings → Health. Its machine `deck` on the tailnet keeps serving until the last step, so the phone works meanwhile. A
server that already has its domain with Caddy needs only the last step. On a Mac with OrbStack or a Linux server
without a domain, first do steps 1–4 of [from a phone, through Tailscale](#from-a-phone-through-tailscale). Then remove
`DECK_TAILSCALE` from `.env`, run `docker compose up -d`, and remove the machine `deck` in the Tailscale admin console
(Machines).

## From a checkout

`sh install.sh` in a checkout of deck pulls master when the checkout is on it, builds the image `deck:local` and runs it in `~/deck` (its
`compose.override.yaml` names that image; remove the line to go back to the published one).

By hand: `docker build --build-arg COMMIT=$(git rev-parse --short=12 HEAD) -t deck:local .` — without `COMMIT` a
build from a git worktree does not know its commit, and `deck.version_get` answers `dev` with `unknown`, why and how to build one that knows it.

## Without Docker

For developing deck: macOS or Linux, Go 1.27.1 or later, and at least one agent CLI (`claude` or `codex`) or an ACP agent on a
local model. Node.js is needed only to rebuild the web client, which is embedded in the binary.

```bash
go build -o deck ./cmd/deck             # add -tags tailnet for -tailscale
./deck                                  # prints http://deck.localhost:7700 and, until a device pairs, the link that pairs one
./deck pair                             # a link that pairs one more device, whether deck runs or not
DECK_HOME=/tmp/deckhome ./deck          # another data folder (default ~/.deck2)
./deck -addr 127.0.0.1:7700             # the listen address (default); the next port serves custom blocks
```

On a Mac without Docker deck runs scripts, MCP servers and ACP agents under `sandbox-exec`, and Claude Code and Codex
outside it, as the owner's. Releases are on [GitHub](https://github.com/teaarte/deck/releases): `deck-<os>-<arch>`
(`-tailnet` with the tailnet) and the image `ghcr.io/teaarte/deck`.

**Updating a binary.** A released binary looks for a new release once a day; when one is out, Settings → Advanced and
a note in the feed show an **Update** button, which only the owner can press. deck downloads its build, checks it
against `release.json` and that file's ed25519 signature with the key built into it, takes a snapshot, puts the new
binary in place and restarts; the agents go on in their sessions. If the new version never gets as far as serving, the
next start goes back to the old one; if a signature or a sha256 does not match, deck stays as it is. A deck built from
source calls itself `dev`, offers no updates, and is updated with `git pull` and `go build`, or, built into the image `deck:local`, with `sh install.sh` in its checkout. On start, deck migrates
its database and the stored manifests.
