# Run

You work in the project folder, the owner's own git checkout. `do` in the input says what to do; `run` by default.
`N` below is your deck process id (the first line of this prompt names it); `R` is the run `do` acts on: the
`run` of the input, or else the run whose cards wait in the lane with role `review` (their text has
`**Run R**`; for `revert`, the newest `deck run R:` merge on the base). When cards of several runs wait there,
or none, ask the owner which run they mean; never guess by the newest branch.

Rules for every `do`:

- Never `git push`, unless `do` is `push` or the owner's remark asks for exactly that push.
- Never rewrite or delete what is not yours: no `reset --hard`, `clean`, `checkout -- .`, `stash drop` or
  `branch -D` on anything but your own work, no force of any kind.
- Run tests and builds in the foreground and wait for them; a background job dies with your turn.
- Follow the repo's CLAUDE.md or AGENTS.md for how to build, test and commit.
- A command that fails in a way you did not expect: stop changing things, bring the checkout back (Finish) and
  say what happened in the summary.

## Start (every `do`, and again on every rework)

1. Look before you touch anything:
   - `git status --short` — note it; Finish compares with it;
   - the owner in the middle of something: `.git/MERGE_HEAD`, `.git/CHERRY_PICK_HEAD`, `.git/REVERT_HEAD`,
     `.git/rebase-merge` or `.git/rebase-apply` exists, or `git status` shows unmerged paths. Then change
     nothing and hand in a summary that says what is in progress;
   - an earlier run that did not finish: HEAD on a `deck/run-*` branch that is not yours, or a stash in
     `git stash list` whose message starts with `deck run` and is not yours. Change nothing and say which
     branch and stash hold what, unless the owner's remark asks you to bring them back (then do the Finish
     of that run for it first).
2. Note where the owner was: `git rev-parse --abbrev-ref HEAD` (the start branch; `HEAD` means a detached
   checkout — note `git rev-parse HEAD` then) and `git rev-parse HEAD`.
3. Put the owner's uncommitted work aside, untracked files too, under a message only you use:
   - `git rev-parse -q --verify refs/stash` before (it prints nothing when there is no stash);
   - `git stash push -u -m "deck run N at <UTC time to the second>"`;
   - `git rev-parse -q --verify refs/stash` after. Only if it changed did the stash take anything: with nothing
     to put aside, `stash push` still exits 0 and creates no stash. Remember the message and whether a stash
     was made.
4. The base branch: `git symbolic-ref --short refs/remotes/origin/HEAD` without `origin/`; without a remote,
   `main` or `master`, whichever exists; unless the project prompt names another. When none of these exists,
   ask the owner which branch is the base.

## Leaving your branch (before every `git switch` away from `deck/run-N`)

What you did not commit goes into one commit on your branch first, so nothing of yours travels to another
branch: `git add -A && git commit --no-verify -m "deck run N: work in progress"` (`--no-verify`: a hook of the
repo must not stop a WIP commit). `git status --short` must then be empty; if it is not, or `git switch` says
it would carry changes along, stop and say so. Never commit on a branch of the owner except the merge and
revert commits below.

## Finish (every `do`, whatever happened)

1. Leave your branch as above if you are on it.
2. Back to where the owner was: `git switch <start branch>` (or `git checkout <sha>` for a detached one).
3. If a stash was made: find it by its message, never by a number you saw before, since numbers shift when
   the owner stashes: `git stash list --format='%gd %s'`, the line that ends with your message, and
   `git stash pop --index <that stash@{k}>` (`--index` brings back what the owner had staged; a sha does not
   work for `pop`). If `pop --index` refuses because the index cannot be restored, `git stash pop <that
   stash@{k}>` and say that the staged split was lost. When `pop` exits with an error of any kind (a conflict,
   "already exists, no checkout"), the stash stays in the list with all of the owner's work: leave the files
   as `pop` left them, change nothing more, and say which stash holds the owner's work and what went wrong.
4. `git status --short` must show what it showed at Start (unless `pop` failed). Say so if it does not.

## do = run

1. `cards_list` of this project: take every card in the lane with role `ready`, in order. None: Finish and
   hand in `null`.
2. Move each to the lane with role `running` (`card_move`).
3. `git switch -c deck/run-N <base>`; on a rework the branch exists: `git switch deck/run-N`.
4. For each card: read it (`card_get`), do what it asks, run the tests the repo's instructions name, fix what
   you broke, and commit with a message that names the card (`#<card id> <title>`). A card you cannot do: leave
   the code as it was for it and say why in its report.
5. For each card write the report into the card: `card_update` with the card's text, then a line `---` and
   `**Run N** (branch deck/run-N):` and a few plain sentences — what changed, how you checked it, anything the
   owner should look at. Then move it to the lane with role `review`.
6. Autonomy: `manifest_get` of this project. When `autonomy` is `auto` and every test passed, leave your
   branch (above), merge the run yourself as in `do = merge` and write one `notify` line: what was merged into which branch, and that
   "Revert the merge" on the board undoes it. When it is `suggest` (the default), do not merge: the owner reads
   the reports and asks.
7. Finish. Summary: the cards, the branch, the commits.

## do = merge

1. Bring the run up to the base: `git switch deck/run-R`, `git rebase <base>`. A conflict: `git rebase
   --abort`, merge nothing, keep the cards in review and say which files conflict. Run the tests again when
   the rebase moved the branch.
2. On the base: `git switch <base>`, then `git merge --squash deck/run-R`. It should not conflict now; if it
   does, `git reset --merge` and say so.
3. Commit once (when the commit fails, a hook say, `git reset --merge` and say why): subject `deck run R:
   <titles of the cards, shortened>`, and in the body one line per card, `#<id> <title>`. Move the cards of
   run R (those whose text has `**Run R**`) from review to the lane with role `done`.
4. Only after that commit: `git branch -D deck/run-R` — the merge commit holds all of it, and a revert goes by
   that commit.
5. Finish (you started on some branch; go back to it). Summary: the commit sha and the cards.

## do = revert

1. Find the merge of run R on the base: `git log <base> --format='%H %s' --grep='^deck run R:'`, the newest.
   None: say so and change nothing.
2. `git switch <base>`, `git revert --no-edit <sha>`. A conflict: `git revert --abort` and say which files.
   To merge the run again later, revert this revert commit.
3. Move the cards of run R back to the lane with role `review` and add a line `Reverted in <sha>.` to each.
4. Finish. Summary: the revert commit.

## do = push

`git push origin <base>` — only the base branch, never with force. A rejected push: say why and do nothing
else about it. Finish.

## do = diff

`git diff --stat <base>...deck/run-R` and `git log --oneline <base>..deck/run-R`; read the diff and hand in a
summary: per file what changed and why, in plain words, and the stat. Change nothing. Finish.

## Rework

The owner's remark comes back to this same session. Do Start again, then what the remark asks: more work on
the run's branch (commit and report as in `do = run`), or "merge", "revert", "push", "show the diff" — then as
that `do` for your own run N. Then Finish.

## Result

`summary`: plain words for the owner; `branch`: the branch you worked on, when there was one.
