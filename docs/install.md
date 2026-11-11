# Installing deck: the details

The short way is in the [README](../README.md#install). This page has the rest: a server with a domain, what the
container needs, behind the host's tailscale serve, Windows, moving a deck that ran without Docker, and deck without Docker.

## A server with a domain

On a Linux server (amd64 or arm64) with a domain name pointing at it and ports 80, 443 and 8443 open, put two lines in
`.env` beside `compose.yaml` (`~/deck/.env`); Caddy in the same compose then gets the HTTPS certificate and serves deck
at that name, with no Tailscale:

```bash
cd ~/deck
printf 'DECK_DOMAIN=deck.example.com\nCOMPOSE_PROFILES=domain\n' > .env
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

deck counts the failed sign-in tries of each client by its address, and takes X-Forwarded-For only from Caddy of
`compose.yaml` (`DECK_PROXY: caddy`). Behind `tailscale serve` of the host every client reaches the container from the
gateway of its docker network, so deck counts them all as one address: thirty steps of signing in a minute together.
The gateway is no proxy of deck, as any container of the host comes from it too; a `.env` or compose of before that
names it (`DECK_PROXY=caddy,gateway`) works, and Settings → Health says to drop `gateway`. Without Docker deck takes
X-Forwarded-For from no address, loopback too, unless `-proxy` names it (`-proxy 127.0.0.1` behind the host's own
proxy).

## Windows

Not tried yet: GitHub's Windows runners do not run Linux containers, so no test covers it. Install Docker Desktop
with the WSL 2 backend, download the files of the README's install by hand into a folder (`curl.exe` in PowerShell, or
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

## Restoring a snapshot

deck keeps its snapshots in its volume, `state/backups/`: `deck-<time>.tar.gz` daily and by hand, `deleted-<time>`
before a module is deleted, and `update-<time>.tar.gz`, the base as it was before a newer deck migrated it on its
first start. An older deck refuses to start on a base a newer one migrated and says so in
`docker compose logs deck`. To go back to the older image, put back the base of the last `update-` snapshot:

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
snapshot goes back the same way, with its whole `state/files` and `projects/*/data` too (`tar -xzf <snapshot>` in
`/var/lib/deck`). Without Docker the same steps run in `~/.deck2` with deck stopped. Back on the newest image, remove
that line `image` from `compose.override.yaml` before the install line runs again.

## From a checkout

`sh install.sh` in a checkout of deck takes its master, builds the image `deck:local` and runs it in `~/deck` (its
`compose.override.yaml` names that image; remove the line to go back to the published one).

By hand: `docker build --build-arg COMMIT=$(git rev-parse --short=12 HEAD) -t deck:local .` — without `COMMIT` a
build from a git worktree does not know its commit, and `deck.version_get` answers `dev` with `unknown`, why and how to build one that knows it.

## Without Docker

For developing deck: macOS or Linux, Go 1.27, and at least one agent CLI (`claude` or `codex`) or an ACP agent on a
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
