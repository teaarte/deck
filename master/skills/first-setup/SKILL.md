---
name: first-setup
description: Walks the owner through the first setup of deck in the chat, one step at a time — access to models (a subscription, an OpenRouter key or a free key), the agents and their sign-in, the assignments of each kind of work, a first module — and names the install command for the owner's system for a program that is missing. Use on deck's first run, when the owner asks to set deck up or says nothing works, and when Today says no model can work as an agent.
---

# First setup by talking

deck installs nothing itself and has no wizard of screens: you lead the owner through the steps in the chat with
the commands there are. One step per message, in the owner's language, plain words, no deck internals. Each step
starts with what is there (a read), says in one line what is missing and why it matters, and gives the one thing to
do — a link to the form of deck, or a command to paste into a terminal. Go to the next step only when the read shows
the step done; a step already done you only name in a line ("Claude Code is signed in — next").

## 0. Where we stand

`health_list` with `machine: true` and the owner's `lang`, `models_list`, `projects_list`, `version_get`. The build
name of `version_get` (`variant`, such as `deck-linux-arm64-…` or `deck-darwin-…`) says deck's system: linux or darwin
(macOS), and the processor. The row `update` of health says when deck runs from its container image: then `variant`
is the container's linux, not the owner's machine — ask which system the machine runs before an install command.
Keep the answer for every install command below.

## 1. Access to models: one of three

deck needs one way to reach models; the owner picks, the first is the recommendation when they have it:

- **A subscription** of Claude or ChatGPT: Claude Code or Codex sign in on it and work as agents — the master and the
  agent commands of modules need an agent. Step 2.
- **An OpenRouter key**: one key, many models, paid and free; a connection of `https://openrouter.ai/api/v1`.
- **A free key** of Groq (`https://api.groq.com/openai/v1`) or NVIDIA (`https://integrate.api.nvidia.com/v1`): free
  text models with limits.

A key goes only into the form of a connection (`connection_set`, `#/deck/settings?tab=Models&dialog=connections`),
which the owner fills in; never ask for a key in the chat. Without a subscription an agent is a strong model of a key
run by goose (step 3).

## 2. Agents: found, signed in, self-check

The rows of kind `provider` and `agent` of health: claude-code, codex, goose.

- `ok` — done.
- `needs_login` of claude-code or codex — the sign-in is by a link: the owner opens Settings → Models
  (`[Sign in](#/deck/settings?tab=Models)`) and presses the sign-in of that agent (`deck.agent_login`, only the owner
  starts it); the page gives a code to enter there, or one to hand back. The self-check runs after it; read health
  again.
- not found — the program is missing: step 6 gives the command. One working agent is enough; say which one the owner
  has and that the other is optional.

## 3. A model, and what a text model is

Say it once, in two lines: an **agent** (Claude Code, Codex, a model in goose) works by itself with files and tools for
many steps; a **text model** of an API answers one request with text, cheaply or for free, and does the small direct
work (a summary, a translation, a check). A text model becomes an agent only through goose, and only a strong one
copes.

A model to add: propose it with `deck.model_set` (connection, model, kind; `agent: goose` only when it should do agent
work) — it waits for the owner's button; say so, with the `link` of its answer, which opens that request. Free ones first when the owner said
so; a paid one only through what the owner chose for paid models. After the yes, the self-check shows in health
whether the model copes as an agent; a weak one stays for text work.

## 4. Assignments by kind

Propose them in one `deck.assignment_set` with `assignments` (one yes of the owner for all), one line of why each:

- `agent` (the master and agent commands): the subscription's agent first; a free model in goose after it, once its
  self-check is `ok`.
- `text`: a fast free or cheap model of a key; the agent's model only when there is no other.
- `stt`, `tts`, `image`, `music`, `video`: a model of that kind — a cloud one of a key, or a server of the owner's
  machine by the models skill; leave a kind unassigned until a module needs it.
- `search`: SearXNG beside deck, assigned on the first run; deck without Docker sets it up by the models skill
  (health of `model:searxng` says when it does not answer).

## 5. A first module

`starters_list` (in the owner's language) gives the modules deck offers on a first run. Offer two or three that fit
what the owner wants, one question with options, the first your recommendation; the one chosen is imported as a
draft and the owner enables it (`[Enable](#/deck/settings?tab=Modules)`). No starter fits, or the owner wants their
own — the module-builder skill. Then `project_readiness_get` of it: what it still lacks (a model, a program, a
right) is the next step.

## 6. A program that is missing

health, `project_readiness_get` or a failed process names a program that is not on the machine. Name the command for
**the owner's system** (step 0), never another's: no `brew` on Linux, no `apt` on macOS. Say where to run it — in a
terminal of the machine deck runs on; a program on a node goes onto that node's machine. After the owner says it is
installed: `program_check` of the project (or health again) and go on.

| program | Linux (Debian, Ubuntu) | macOS |
|---|---|---|
| Claude Code | `curl -fsSL https://claude.ai/install.sh \| bash` | `curl -fsSL https://claude.ai/install.sh \| bash` |
| Codex | `npm install -g @openai/codex` | `brew install codex` |
| goose | `curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh \| CONFIGURE=false bash` | `brew install block-goose-cli` |
| uv | `curl -LsSf https://astral.sh/uv/install.sh \| sh` | `brew install uv` |
| node | `sudo apt install -y nodejs npm` | `brew install node` |
| bubblewrap | `sudo apt install -y bubblewrap` | not needed (macOS has its own sandbox) |
| ffmpeg | `sudo apt install -y ffmpeg` | `brew install ffmpeg` |
| unzip | `sudo apt install -y unzip` | there by default |

Another Linux: `dnf install` (Fedora), `pacman -S` (Arch) with the same package names; when you do not know the
distribution, ask one question with these as options, Debian/Ubuntu first. macOS without Homebrew: first
`/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`. A program not in the
table: its official install page (`master.research`), never a copy from elsewhere. deck in its container image: the
program belongs in the image, not in the running container, where a restart loses it.

## Rules

- You never set a key, add a folder, enable a module or press the owner's button: you give the link with the values
  and say what the button does.
- No other service or key in place of the one missing; nothing works around a refusal.
- At the end, one line of what is set up and what is left; keep what the owner chose (memory skill) when it should
  hold later, such as "free models first".
