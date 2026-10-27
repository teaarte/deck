<div align="center">

# deck

### A personal control panel for AI agents, running on your own machine.

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-one_file-003B57?logo=sqlite&logoColor=white)
![MCP](https://img.shields.io/badge/MCP-native-black)
![PWA](https://img.shields.io/badge/phone-PWA-5A0FC8)

<img src="docs/screenshots/tiles.png" alt="The tiles of deck: Today with one decision waiting, module cards with live widgets" width="900">

</div>

## What it is

deck is one program with one data folder. It runs your routines as **modules** and hands the work to agents:
Claude Code, Codex, or an open model in an ACP agent (Goose), each run in a sandbox. You see the results, and you
decide only what needs your yes.

It is for one person who already uses coding agents and wants them to do recurring work, such as reading sources,
watching prices, reviewing pull requests or making short videos, without sitting in a chat for each run.

A module is data, not code in deck: a manifest with collections of records, commands, schedules, screens and
widgets, plus prompts and small scripts. **Deki**, the built-in master agent, builds a module from a short
conversation and changes it later.

## What it does

- **Tiles and Today.** The home screen is a grid of cards you place and resize. Each module card shows a widget
  of its data. The **Today** card shows what waits for you, what is running, what was done, today's spend and the
  next scheduled run.
- **Deki in a side chat.** Deki opens beside any screen and sees that screen. While it works, a status line shows
  the current step. Its questions come one at a time as cards, each option with a short explanation.
- **Modules with their own screens.** These include tables, lists, boards, cards, forms, stats, charts, media and
  a file manager, with an optional side panel of filters and stats. Anything the catalog lacks is a sandboxed
  `custom` block.
- **A yes only where it matters.** deck asks before anything that leaves the machine, spends money or widens a
  module's rights. A button you press already counts as your yes to what its row shows. Changes made without you,
  such as deleted records, edited documents or new assignments, are listed under **Done without you**, with an
  undo for each run.
- **Any model, per command.** Settings → Models has a "who does what" table, where you pick the model of each
  command in its row, and a grid of every model grouped by provider. It covers subscriptions (Claude Code,
  Codex), OpenRouter, Google, Anthropic, Groq and any OpenAI-compatible or local server. Each model has prices and
  fallbacks. **Spend** shows what each model and module cost, with subscriptions kept separate from money paid.
- **Schedule, Files, Look.** Triggers with their next run, the files of every module, and themes with light
  and dark schemes, accent and density.
- **Tools with secrets.** A module calls HTTP APIs and MCP servers through deck. Keys live in deck's secrets, and
  a private link, such as a calendar feed URL, can be a secret limited to exact hosts. deck keeps secrets out of
  logs, errors and answers.
- **Sandbox and one way out.** Scripts, MCP servers and ACP agents run under `sandbox-exec` on macOS or
  bubblewrap on Linux. Network access goes only through deck's proxy, which reaches only the domains a module
  declares.
- **Phone and desktop.** On a phone, deck installs as a PWA with pushes and a share target. It can also join your
  Tailscale network over HTTPS. A tray launcher runs deck as `deck.app` on macOS, and on Windows inside WSL2.

## A look inside

<table>
<tr>
<td width="50%"><img src="docs/screenshots/today.png" alt="Today: a decision to allow, what runs, what was done without you with an undo"><br><sub><b>Today.</b>
One decision waits; a run's deletions sit under "done without you" with an undo.</sub></td>
<td width="50%"><img src="docs/screenshots/chat.png" alt="A board module with Deki's chat open beside it"><br><sub><b>Deki beside a module.</b>
The chat sees the screen and offers its commands.</sub></td>
</tr>
<tr>
<td width="50%"><img src="docs/screenshots/module.png" alt="A module screen with a side panel of filters and stats, a chart and a table"><br><sub><b>A module screen.</b>
Side panel with a filter and stats, a chart and a table, all from the manifest.</sub></td>
<td width="50%"><img src="docs/screenshots/board.png" alt="A module as a board with lanes"><br><sub><b>A board.</b>
Records in lanes by their status; dragging a card runs the module's command.</sub></td>
</tr>
<tr>
<td width="50%"><img src="docs/screenshots/models.png" alt="Settings, Models: who does what, a model per command"><br><sub><b>Models.</b>
The model of each command picked in its row, with price and fallbacks.</sub></td>
<td width="50%"><img src="docs/screenshots/tiles-dark.png" alt="The tiles in the dark scheme"><br><sub><b>Dark scheme.</b>
The same tiles; the theme follows the system or your choice.</sub></td>
</tr>
<tr>
<td width="50%" align="center"><img src="docs/screenshots/phone.png" alt="The tiles on a phone" width="240">
<img src="docs/screenshots/phone-today.png" alt="Today on a phone" width="240"><br><sub><b>On a phone.</b>
Tiles and Today, with a tab bar for the thumb.</sub></td>
<td width="50%"><img src="docs/screenshots/first-run.png" alt="An empty deck: tell Deki what to do, or start with a module"><br><sub><b>First run.</b>
Tell Deki what you need, or add a starter module as a draft.</sub></td>
</tr>
<tr>
<td width="50%"><img src="docs/screenshots/first-run-draft.png" alt="A draft module: what it needs to work and what it may do"><br><sub><b>A draft before enabling.</b>
What the module needs and what it may do once enabled.</sub></td>
<td width="50%"></td>
</tr>
</table>

## Install and run

You need macOS or Linux, Go 1.27, and at least one agent CLI (`claude` or `codex`). Node is needed only to
rebuild the web client, which is embedded in the binary.

```bash
go build -tags tailnet -o deck ./cmd/deck
./deck                                  # prints the address with the access token; data in ~/.deck2
DECK_HOME=/tmp/deckhome ./deck          # another data folder
./deck -addr 127.0.0.1:7700             # the listen address (default); the next port serves custom blocks
./deck -tailscale deck                  # join your tailnet as "deck" and serve HTTPS there
```

Without `-tags tailnet` the binary is smaller and `-tailscale` refuses to start. To update deck, replace the
binary. On start, deck brings the database and the stored manifests up to the current format.

**Desktop app.** `desktop/build.sh` builds `deck.app` for macOS and `deck-setup.exe` for Windows (it needs
`brew install makensis`). The launcher sits in the tray, starts deck, opens it in its own window and can start at
login. On Windows, deck runs in its own WSL2 distribution, which the installer sets up (not yet tried on a real
Windows machine).

**Phone.** Open deck's address in Safari or Chrome, enter the token once and add it to the home screen. To reach
deck away from home, run it with `-tailscale`.

## More

- [docs/architecture.md](docs/architecture.md): how deck is built, what a module looks like, build and tests.
- [CLAUDE.md](CLAUDE.md): the package map and the rules of the code.

## License

[PolyForm Noncommercial 1.0.0](LICENSE.md): free to read, run, change and share for any noncommercial purpose.
Commercial use needs a separate license from the author.
