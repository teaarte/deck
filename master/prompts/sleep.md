Deki's sleep: once a night, go over what happened since the last sleep and keep the memory of deck true, short
and useful, by the memory skill. Use your judgement of what matters; most nights change little.

1. Your lesson `sleep-last` (the owner's memory, project master, `command: sleep`) says when the last sleep ended;
   without it, look back one day.
   `episodes_list` with `since` that time: the processes that ended (failed ones with their error), incidents, the
   owner's messages in chat topics and the summaries of topics that changed.
2. Check the memory against them: the owner's memory and that of each project the episodes touch
   (`documents_list` kind `memory`, `document_get`). A fact an episode shows wrong — the owner said otherwise, a
   project changed — gets corrected (it keeps its history in revisions); a lesson gets `source` with the episodes
   that show it.
3. Write lessons where an episode would change how the next run goes: a command that failed the same way again, a
   way around that worked, a correction of the owner in a topic about how Deki works (a correction of what the owner
   wants is a fact of the owner's words, not a lesson). A lesson of one command names it in `command`; it goes to the
   memory of that command's project. Search before you write (`memory_search`): edit a lesson on the same thing,
   do not add another.
4. Tidy: merge duplicates, delete facts and lessons whose `expires` passed and lessons the episodes no longer bear
   out; keep each index under 150 lines, the owner's words first, the Lessons section short; what matters most goes
   up, what no longer matters goes down or out of the index (the fact stays, `memory_search` finds it).
5. Once a week (`sleep-last` says when you last did): lessons of one command or skill that keep
   coming back are worth a change of its skill — propose it with `skill_propose`, the whole new text and why; the
   owner decides. One proposal a skill; none when nothing repeats.
6. Update your lesson `sleep-last`: when this sleep ended (`source: process:<this process>`), and the date of
   the last weekly pass.
7. `memory_overview` gives the numbers: write one `owner_notify` only when something changed, one line: facts
   corrected, lessons written, deleted, proposals — and the repeats of failures from the overview.

The result is a short report: what changed (facts corrected with their source, lessons written, deleted, merged,
proposals), and the numbers of `memory_overview`; or that nothing needed a change.
