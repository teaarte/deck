# Nodes

deck in a container — on a server, or on a Mac in OrbStack or Docker Desktop — does not reach the models of a machine
that listen on its own `127.0.0.1` (whisper, servers of pictures, music and speech, Ollama, SearXNG), nor a Mac's
GPU, its agents' logins or its folders. A **node** serves them: the same `deck` binary on that machine, outside
Docker, holds one connection out to deck and carries deck's calls to its loopback. It needs no Tailscale and opens no
port; it reaches nothing of its network but its own loopback, and its key opens nothing of deck but that connection.

## Pairing a node

1. On deck's machine run `deck pair --node --name Mac` (in Docker: `docker compose exec deck deck pair --node --name
   Mac`), or pair a node in Settings → Devices. It prints a command with a code that works once, within ten minutes.
2. On the node's machine, download the plain binary of [the release](https://github.com/teaarte/deck/releases) for
   it (`deck-darwin-arm64`, `deck-linux-amd64` or `deck-linux-arm64`, not the `-tailnet` one) into a folder it stays
   in, since the service runs it from there, and make it executable:

   ```bash
   mkdir -p ~/.local/bin && cd ~/.local/bin
   curl -fsSLo deck https://github.com/teaarte/deck/releases/latest/download/deck-darwin-arm64 && chmod +x deck
   ```

   The binary is not signed by Apple: downloaded with `curl` it runs; downloaded in a browser, macOS blocks it until
   `xattr -d com.apple.quarantine deck`.
3. Run the command of step 1 with that binary, adding `--allow` and what deck may use there: the ports of its models,
   its agents and folders, such as
   `./deck -join https://deck.example.com <code> --install --allow 8178,8190,claude-code,~/code/repo`
   (for deck on the same Mac, `./deck -join http://127.0.0.1:7700 <code> --install …`).
4. In Settings → Models → Connections set **Node** of each connection the node's machine serves, its address as on
   that machine, such as `http://127.0.0.1:8178` for whisper.

The node keeps the list of what it allows itself, in `~/.deck2-node/state/node/allowed` (an edit there holds at once);
deck never widens it, and a call to anything not on it is refused, with a row in Settings → Health of what to add. The
key is in `~/.deck2-node/state/node/joined.json` (readable by you alone), the log in
`~/.deck2-node/state/node/node.log`. `--install` installs a service that starts the node when you sign in and keeps it
connected: a LaunchAgent `deck-node` on macOS, a systemd unit of your user on Linux
(`~/.config/systemd/user/deck-node.service`; on a server without a session of yours, `loginctl enable-linger`).
Without `--install` the node runs in the terminal until Ctrl-C. `deck -join --uninstall` removes the service; revoking
the node in Settings → Devices ends its connection at once and refuses its key. A node joined before into `~/.deck2`
moves its key into its own folder with `deck -join <address> --install --allow …` once.

While the node is off, a call of its model goes at once to the next model of its fallbacks; Settings → Health has a row
of each node: online or not since when, what it serves, and the command to run on its machine. A big answer of a model
of a node, a picture or a sound, streams to deck and is kept there; the row `disk` of Health warns before deck's disk
fills.

## Agents on a node

A node runs deck's agents on its own machine and sign-in, such as Claude Code on the owner's subscription of a Mac
while deck lives on a server. Allow them on the node (`--allow claude-code,codex`, or an ACP agent such as `goose`);
deck then offers `claude-code@Mac` (and each model of the CLI `@Mac`) in Settings → Models while the node is online,
and an assignment picks it as any other agent, with fallbacks. The sign-in of `claude-code@Mac` runs on the node, its
link comes to deck.

A turn on a node works in the node's copy of the turn's folders (up to 2 GB), reaches deck's tools, questions and the
model of an ACP agent through deck by the node's key, and brings the files it changed back to deck at its end. On a Mac
node the CLIs run outside a box, so an agent allowed on a Mac node acts as its user: allow it only on a machine you
trust deck with. If the node goes away in the middle of a turn, the turn waits for it three minutes, goes on in its
session if the node comes back, and otherwise goes to the next agent of the assignment.

## Folders on a node

A project folder can be on a node's machine, such as a repository deck in a container does not see. Allow it on the
node by its path (a path begins with `/` or `~` and allows what is inside it; the node's own folder and `~/.deck2` are
never allowed), then add it to the module in its settings, Access, naming the node. deck asks the node whether the
folder is there and allowed; once the owner takes one off the list, the turn or script that needs it is refused and
Health says so in the row of the node.

- An agent command that works in a folder of a node runs on the agent of that node (`claude-code@Mac` for an
  assignment of `claude-code`), in the folder itself; what it commits or pushes stays in that repository. A git
  repository may have a clone on each machine under one name: the turn goes where a clone is, an online one first.
- The scripts of a module whose folders are on a node run on that node, in its box; no field of the manifest says
  where, the folders do.
- The module's records and files stay on deck: a run on a node gets a copy of its run folder and of the module's data,
  and what it changed comes back at its end. Runs of one command take turns there, even with `"runs": "many"`; a file
  that changed on deck meanwhile stays as it is on deck, and the run's log names the changes that did not come back.
- Removing a folder in Access takes it from the one machine of its row; revoking a node takes its folders from every
  module.
- While the node is off, a turn or script of its folder waits a few minutes, runs once it is back, and otherwise a turn
  goes to the next agent and a script fails saying the node did not come back.
