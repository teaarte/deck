---
name: code-project
description: Connects one of the owner's code repos to deck as a project whose agent works on the cards of its board in the repo and hands them back for review; optionally takes issues of a GitLab project as cards. Use when the owner wants deck to work on a code repo.
---

# Connecting a code repo

A code project is an ordinary deck module (see the module-builder skill for the manifest and the tools): a
board, an agent command that works in the project folder, and md that says how to run. deck itself knows
nothing of git. It gives the command the folder, sees that it is a repo (the agent gets the repo's
CLAUDE.md or AGENTS.md like a session in a terminal), and lets only one agent work in a folder at a time: a
second one waits ("waits for folder …"). Everything else — stash, branch, commits, going back, merge,
revert, push, diff — is the agent following its md.

The module to start from is `examples/code` of the deck source: `manifest.json`, `prompt.md`,
`prompts/run.md` (how a run goes), `scripts/follow_gitlab.py` and `scripts/poll_gitlab.py` (GitLab intake).
When it is not at hand, write the same shape yourself from this skill.

## 1. Ask the owner

One question at a time, the first option your recommendation:

- which repo (its absolute path), and whether its CLAUDE.md or AGENTS.md says how to build, test and commit —
  a run follows those; if they are missing, the repo needs them first (offer to write them with the owner);
- the base branch: the repo's default (recommended), or another one;
- autonomy: `suggest` (recommended; the agent hands cards back in Review and merges only when asked) or `auto`
  (it merges itself after its own checks and says so in the cockpit feed);
- which provider runs it: Claude Code or Codex; the owner sets it in Settings → Providers. A folder that is a
  git worktree (its `.git` is a file) works only on Claude Code: Codex may not write the git folder of a
  worktree, which lies outside it;
- where tasks come from: the owner's own cards (always), and a GitLab project (its path; labels, when only
  issues with every one of them should come).

## 2. Build

1. Copy the example into a new folder and change only what the answers need: `prompt.md` (its first line
   describes the project; name the base branch there when it is not the default one; repo facts the agent
   should know), `"autonomy": "auto"` in the manifest when asked, the GitLab host in the URL of
   `gitlab_issues` when it is not gitlab.com. A repo without GitLab intake drops the `intake` collection, the
   commands and scripts `follow_gitlab` and `poll_gitlab`, the trigger, the tool, the Intake screen and
   `intake` in `menu`.
2. `project_import` the folder. It is a draft until the owner enables it.
3. Tell the owner what only they do, on the Settings screen:
   - Projects → "Add folder": the project and the repo path (a folder lets agents write there, so only the
     owner adds it);
   - Secrets: `gitlab`, a GitLab access token with `read_api` (when there is intake);
   - Providers: the provider of `<project>.run`;
   - Projects → "Enable".
4. With intake: the owner (or you, once enabled) runs "Follow a GitLab project" on the Intake screen; issues
   of the last 14 days come first, then every 15 minutes what is new. Following it again with other labels
   keeps the cursor: issues older than it do not come.

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
- Merge (squash into the base, one commit listing the cards), revert (of that commit), push and diff are the
  same command with `do`, from the buttons of the board screen, or a remark on the run ("merge it") sent back
  to the same session.
- Tests run in the foreground: a background job dies with the turn.

## 4. Try

A trial of `run` is a real run in the owner's repo: internal actions, cards and git are real. Try it with the
owner on one small card, or on `do: diff`, and read the card's report and `git log` with them. Check that
`git status` shows the owner's uncommitted work as before.

## 5. Hand over

Say what the project does, what waits for the owner (folder, secret, provider, enable) and how they start a
run: cards into Ready, "Run" on the board screen, then read the reports in Review and press "Merge" or send a
remark.

## 6. Improve this skill

When the owner fixes a code project by hand or a run went wrong because this skill or the example was silent,
fix the rule here (`document_save`, kind `skill`, name `skills/code-project/SKILL.md`, no project) in the
words of its section; no dated notes. Say what you changed.
