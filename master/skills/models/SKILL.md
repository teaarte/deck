---
name: models
description: How deck reaches a model — a ready open-source server or a cloud API, never weights in deck. Which server serves a kind, how the owner installs it on a Mac or Linux, how it becomes a connection (a node or a local address), cloud or own by how often the work calls it, and how to try a model through the cloud. Use when work needs a model deck lacks, the owner wants a model on their own machine, or a model is to be tried.
---

# Models: a ready server or the cloud

deck raises no model inside itself: it talks to models and keeps their keys, pauses, fallbacks and spend. A model is
a connection — a cloud API, or a ready open-source server the owner installs and runs on a machine of theirs.

- Weights never live in deck: not in a module's data, not in your project, not for a while. No script of a module or
  of yours downloads them, and no one writes a server or a handler around a model — not you, not the owner. deck
  warns of a module that looks like it does (gpu with torch, mlx or CUDA in a PEP 723 header, a download from
  Hugging Face).
- A small model a package computes on the CPU (a depth map of 100 MB) is a dependency of the script, not a model: its
  files go to the script's cache; deck still warns of their download from Hugging Face, and the warning stays.
- No ready server speaks the API of a model (OpenAI-compatible, or one deck knows) — the work goes to a cloud API,
  or it is not done.

## A server by kind

| kind | ready servers |
|---|---|
| text | Ollama, llama.cpp (`llama-server`); vLLM on Linux with an NVIDIA card |
| stt | whisper.cpp (`whisper-server --inference-path /v1/audio/transcriptions`) |
| tts | mlx-audio, Rapid-MLX on a Mac; LocalAI on Linux |
| image | mlx-openai-server on a Mac; LocalAI on Linux |
| search | SearXNG (a connection of api `raw` on its address without `/v1`, a model of kind `search`) |

Music and video: a cloud API unless a ready server of the kind appears. Read the server's own page
(`master.research`) for its install and its model names before you give the steps.

## The owner installs it

You give the steps for the owner's system (the first-setup skill finds it); the owner installs and starts the
server. The server keeps its models in its own folder (Ollama in `~/.ollama`), never in deck's.

- Mac: `brew install <server>` and a service that starts with the machine (`brew services start ollama`, or a
  launchd plist of the program's page).
- Linux: the package or the release binary and a systemd unit; or a service in `compose.override.yaml` beside deck's
  `compose.yaml` (an update downloads `compose.yaml` again, the override stays).

## The connection

- The server on deck's machine, deck without Docker: `http://127.0.0.1:<port>/v1`, `local`.
- deck in its container, the server a service of the same compose: `http://<service>:<port>/v1` by its name.
- The server on another machine, or on deck's own host while deck runs in its container, with a node of deck
  (`deck -join`) on that machine: the connection names that node and the address
  `http://127.0.0.1:<port>/v1` of that machine; the node passes only the ports on its list. It works across
  networks without an open port.
- The server on another machine of the same local network: its address there, `http://192.168.1.20:11434/v1`; the
  network and the open port are the owner's. deck in its container on `127.0.0.1` without a node reaches the
  container itself, and health says so.

Then the model: `deck.connection_set` is the owner's form (give its link with the values), `deck.model_set`
(connection, model as the server names it, kind) waits for the owner's button, and an assignment of its kind
(`deck.assignment_set`).

## Cloud or own: by how often

A rare task of a niche model — a cloud API, under `money` of the module. A task that runs often — a model of the
owner's machine, when a ready server for it exists. The owner chooses. Count with `spend_list` (calls and cost by
project, command and model) and `spend_days_list` (by day): a paid model a command calls every day for weeks you name
in a note of the feed (`owner_notify`) with its calls and cost and the server that would take it; the owner decides.

## Trying a model

"Will this model do for the task" is answered through a cloud API (Replicate, fal, Hugging Face Inference,
OpenRouter): the model proposed with `deck.model_set`, then a few calls of `deck.model_call`, each of a paid model
waiting for the owner's yes; or, for a run of many calls, a probe module the owner enables with `money` on that model
and a `run_usd` cap (a draft reaches no paid model). When the answer is in, remove the paid model and the probe. A model no cloud serves is tried only through its ready server:
the owner installs it by your steps, then a connection. Never download weights to try one.
