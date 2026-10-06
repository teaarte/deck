---
name: code-project
description: Connects one of the owner's code repos to deck as a project whose agent works on the cards of its board in the repo and hands them back for review; adds routines to such a project, such as taking tasks from a tracker (GitHub, GitLab, Jira, …). Use when the owner wants deck to work on a code repo or asks for a routine in a code project.
---

# Connecting a code repo

A code project is an ordinary deck module (see the module-builder skill for the manifest and the tools): a
board, an agent command that works in the project folder, and md that says how to run. deck itself knows
nothing of git. It gives the command the folder, sees that it is a repo (the agent gets the repo's
CLAUDE.md or AGENTS.md like a session in a terminal), and lets only one agent work in a folder at a time: a
second one waits ("waits for folder …"). Everything else — stash, branch, commits, going back, merge,
revert, push, diff — is the agent following its md.

The module to start from is in the `example` folder of this skill: `manifest.json` (board and the `run`
command), `prompt.md` and `prompts/run.md` (how a run goes). Copy it into your working folder, adapt it and
`project_import` it. Routines the owner asks for later (section 5) you add to the project's manifest.

## 1. Ask the owner

One question at a time, the first option your recommendation:

- which repo (its absolute path), and whether its CLAUDE.md or AGENTS.md says how to build, test and commit —
  a run follows those; if they are missing, the repo needs them first (offer to write them with the owner);
- the base branch: the repo's default (recommended), or another one;
- merging: only when asked (recommended; the agent hands its work back for review) or by itself after its own
  checks, saying so in the cockpit feed;
- which provider runs it: Claude Code or Codex; the owner sets it in the Settings of the project.

The folder is the root of the repo (where `.git` is), not a folder inside it and not a git worktree.

## 2. Build

1. Copy the example into a new folder and change only what the answers need: `prompt.md` (its first line
   describes the project; name the base branch there when it is not the default one; repo facts the agent
   should know), and the line `Merge: yourself.` when the agent merges by itself.
2. `project_import` the folder. It is a draft until the owner enables it.
3. Tell the owner what only they do, in the Settings of the project (Settings → Projects → "Settings" on its
   row):
   - "Add folder": the repo path (a folder lets agents write there, so only the owner adds it);
   - "Models" on the row of `<project>.run`: the model it runs on;
   - "Enable".

## 3. How a run goes (what `prompts/run.md` must keep)

- The agent works in the owner's own checkout. At the start it puts uncommitted work aside with
  `git stash push -u -m "<message only it uses>"` and compares `git rev-parse -q --verify refs/stash` before
  and after: with nothing to put aside `stash push` exits 0 and makes no stash. At the end it finds its stash by
  that message in `git stash list` — stash numbers shift when the owner stashes, and `stash pop <sha>` does not
  work — and pops it with `--index`, so what was staged stays staged; a failed pop leaves the stash in the list
  and goes into the summary. It starts nothing while the owner is in the middle of a merge, rebase or
  cherry-pick, or while an earlier run left its branch checked out or its stash behind.
- It works on branch `deck/run-<its process id>` from the base, commits per card, writes a report in plain
  words into each card and moves it to Review; before it leaves its branch a WIP commit (`--no-verify`) takes
  what it did not commit; then it returns to the branch the owner was on. What the owner edits in the same
  checkout while a run works lands in the run's branch: the run holds the checkout.
- `git push` only when the owner asks for it (a `push` button or a remark). That rule is text only: the
  agent's shell could push, and the owner accepted that.
- Merge: the run's branch is rebased on the base first (a conflict stops it there), then squashed into the
  base as one commit listing the cards, and the branch is deleted after that commit. Revert reverts that
  commit. Merge, revert, push and diff are the same command with `do`, from the buttons of the board screen,
  or a remark on the run ("merge it") sent back to the same session. A button pressed with nothing to do
  still costs a short turn of the model; the agent answers that there is nothing to do.
- Tests run in the foreground: a background job dies with the turn.

## 4. Try

A trial of `run` is a real run in the owner's repo: internal actions, cards and git are real. Try it with the
owner on one small card, or on `do: diff`, and read the card's report and `git log` with them. Check that
`git status` shows the owner's uncommitted work as before.

## 5. Routines

The owner asks in a phrase ("take the open issues of github.com/team/app as cards"); you add it to the
project the way the module-builder skill builds any routine, then try it and hand it over. A routine that
brings tasks from a tracker:

- a secret for the tracker's token (the owner sets it in Settings → Connections → Other secrets; say its name and the scope it
  needs, read-only when it only reads);
- an HTTP tool from the tracker's API docs that lists open issues created since a time, oldest first, signed
  in with that secret, class `internal` (it only reads);
- a script on a `cron` trigger that keeps a cursor in a record: the creation time of the newest issue taken and
  the ids taken at that very time (a "since" filter usually includes that time); each page is asked again from
  the moved cursor, so an issue closed meanwhile shifts nothing past a page; one `card_create` per new issue
  with its text and link, the cursor written after each card; any failure is an error of the script;
- the first poll takes a window the owner agrees to (the last two weeks, say); ask which issues come (all
  open ones, or with a label; mind what the API means by several labels);
- the tracker gets nothing back: the owner closes an issue there, or a commit message closes it on push.

The same shape fits other routines of a code project (a nightly dependency check, a report of red tests): a
script when it is mechanics, an agent in the folder when it needs judgement.

## 6. Hand over

Say what the project does, what waits for the owner (folder, secret, provider, enable) and how they start a
run: cards into Ready, "Run" on the board screen, then read the reports in Review and press "Merge" or send a
remark.

## 7. Improve this skill

When the owner fixes a code project by hand or a run went wrong because this skill or the example was silent,
fix the rule here (`document_save`, kind `skill`, name `skills/code-project/SKILL.md`, no project) in the
words of its section; no dated notes. Say what you changed.
