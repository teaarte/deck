---
name: module-conventions
description: The conventions of the code of a deck module — its layout, the contract and style of its scripts, its agent prompts, how it is checked and tried, and what a review of a change looks for. Use when writing, changing or reviewing the scripts, prompts or manifest of a module.
---

# Conventions of a module's code

A module is a deck project: a manifest, md documents, scripts, skills and custom blocks. The module-builder skill
says what each part of a manifest does and how deck runs it; this one says how its code is written, so every
module reads the same and a reviewer knows what to look for.

## Layout

- `manifest.json` — the module; `prompt.md` — its first line is the one-line description, then what it is for.
- `prompts/<command>.md` — the prompt of each agent command; facts and criteria in `<topic>.md` it reads.
- `scripts/<command>.py` (or `.mjs`, `.sh`) — one script per script command, named as the command.
- `skills/<name>/SKILL.md` — a way of working an agent of the module reuses; `custom/<name>.html` — a custom block.
- The data folder (`$DECK_DATA`): `<collection>/<key>/` for what a record has, `cache/` for what can be made again,
  `.cache` for package managers; nothing loose at its root. State that must survive a trial's undo is a record.
- Names: commands and collections in `snake_case` verbs and plural nouns (`fetch`, `score`, `items`), fields in
  `snake_case`, never a name of an expression function (`date`, `count`, `len`) — `day`, `ops`.
- Texts of screens and commands in English with `i18n` for the owner's language.

## Scripts

The contract, the same for every runtime:

- Input JSON on stdin, result JSON on stdout — one document, nothing else on stdout; progress and logs to stderr.
- Nothing to do is a result (`null`, or `{"new": 0}`), not a failure.
- A failure exits non-zero with one line on stdout:
  `{"error": "<one line for the owner>", "type": "<type>", "details": <what explains it>}`. Types: `script.failed`
  (a bug, the default), `input.invalid`, `program.missing`, `network`, `rights.denied`, `model.limit`,
  `model.unavailable`. Helpers raise; only the entry point prints the failure, so stdout never holds two documents.
- Take the part above "Example" of `helpers/deck_helper.py` (or `.mjs`) of the module-builder skill into the script
  rather than writing your own: the failure form, calls of deck with deck's answer as details, a file replaced
  whole under its lock. A script runs alone and cannot import a file beside it.

Style:

- Python by default: `"runtime": "exec"`, `#!/usr/bin/env -S uv run --script` and a PEP 723 block with pinned
  versions (`httpx==0.28.1`); the standard library when it is enough. Node: `npm install --prefix $DECK_DATA/node`
  of a pinned version, output to stderr.
- A `main(inp)` that returns the result, small functions under it, constants at the top; no state in globals
  between functions, no `print` in helpers.
- Every network call has a timeout and goes through the proxy deck sets (`HTTP(S)_PROXY`); its hosts are in
  `domains.read`. A failed page of a service is `network` with the status and the start of the body in `details`.
- Calls of deck: `$DECK_URL/commands/<command>` with `Authorization: Bearer $DECK_TOKEN`; a refusal's body says
  what is missing — keep it in `details`. Models only through `$DECK_MODELS` or `deck.model_call`, never a key.
- Idempotent: records by a key that identifies an item for good (`records_upsert`), a cursor of the source kept
  in a record, so a second run after a lost one writes nothing twice.
- Many records are a script's work: filter, batch and merge in code; an agent gets only the slice it judges.
- A file of `$DECK_DATA` rewritten whole goes through a temporary name of its own run and `os.replace`; a
  read-modify-write holds the lock for the whole of it.
- Runs that share more than one file — an install into `$DECK_DATA`, an engine, a browser, a merged work file —
  are one at a time: `"runs": "one"` on the command, and deck queues the next call; no lock of your own.
- No secrets, keys or the owner's paths in code: a key is an HTTP tool of the manifest with `$secret.<name>`, a
  folder of the owner is read from a record of settings.
- Files the owner should see go to `$DECK_RESULTS` and are named in the result (`files`); scratch files stay in
  the run folder.
- Short comments that say what a function gives, not how; no commented-out code.

## Agent prompts

- Say what to do with the input, which deck tool to call for what, and what the result means; the result is
  handed in at the end, by its schema.
- Facts and criteria in md documents the prompt names, not in the prompt; a reusable way of working is a skill.
- Write for a small model too: the shape of the result without sample values, every check a pattern can do left
  to a script whose findings come in the input, and the script, not the verdict, decides what reaches the owner.
- An agent that reads what strangers wrote gets `calls`, the list of commands it may call.

## Check and try

1. `manifest_check` before every save; fix what it refuses and its `untranslated` and `unused_i18n`.
2. `project_import` the folder as a draft; later changes with `manifest_save` and `document_save`, `old` and
   `new` only, and the folder kept in step.
3. `command_try` each command, the chain after it too; `process_get` gives the result and in `left` what the trial
   wrote; `process_log_get` with `only: errors` what failed. A run that ends `done` is checked by what it made.
4. `screen_capture` each screen on a phone and a computer; `project_needs_get` says what the module still lacks.
5. Delete what the trials left that the owner should not start with, and say what you kept.

## Review of a change

Read the diff against these conventions and the module-builder skill; report only what breaks or will break,
each with the file, the line and the case that fails:

- the contract of a script: stdout holds only the result, a failure has the one form and a fitting type, nothing
  to do is not a failure;
- a call without a timeout, a host outside `domains.read`, a key or path in code, a model called around deck;
- a second run writes twice: no key, no cursor, a temporary file shared by runs, a read-modify-write without lock;
- two runs at once break each other: shared installs or work files without `"runs": "one"`, a lock of the module's
  own where `"runs": "one"` would do;
- the manifest: an expression that breaks on a record without a field, a text without `i18n`, a right wider
  than the work needs (`external` tools that only read, `"*"` domains);
- a prompt that leaves the result shape or the tools to guess, or asks a small model to check what a script can;
- a trial that was not run, or that ended `done` without what the owner will see.

Do not report style that is a matter of taste, nor what the change did not touch.
