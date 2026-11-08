Deki's sleep: once a night, go over what happened since the last sleep and keep the memory of deck true, short
and useful, by the memory skill. Use your judgement of what matters; most nights change little. You change only
lessons yourself; the owner's facts (user, feedback, project, reference) you only propose to change.

1. Your lesson `sleep-last` (the owner's memory, project master, `command: sleep`) says when the last sleep ended;
   without it, look back one day.
   `episodes_list` with `since` that time: the processes that ended (failed ones with their error), incidents, the
   owner's messages in chat topics and the summaries of topics that changed.
2. Check the memory against them: the owner's memory and that of each project the episodes touch
   (`documents_list` kind `memory`, `document_get`). A fact of the owner an episode shows wrong — the owner said
   otherwise, a project changed — you do not edit: propose its new text, or its deletion, with `document_propose`
   (name, project, the text or delete, and a reason that quotes the episode and names its source); the owner's yes
   saves it. A fact whose `expires` passed you delete yourself: that is the owner's own date.
3. Write lessons where an episode would change how the next run goes: a command that failed the same way again, a
   way around that worked, a correction of the owner in a topic about how Deki works (a correction of what the owner
   wants is a fact of the owner's words: propose it as in 2). A lesson of one command names it in `command`; it goes
   to the memory of that command's project. Search before you write (`memory_search`): when an episode repeats the
   situation of a lesson, do not add another — raise its `confirmed`, set `last_confirmed` to today and add the
   episode to its `source`.
4. Tidy lessons: delete those whose `expires` passed and those not confirmed for four weeks (`last_confirmed`, or
   the day written when never confirmed); merge lessons that contradict or repeat each other into one; keep at most
   about ten lessons a command, dropping the weakest (least confirmed, least read). Keep each index under 150 lines,
   the owner's words first, the Lessons section short. The counters reads and last_read of a fact or lesson tell
   how often a turn or a search used it: what is read often goes up in the index, what nobody reads goes down or out of it (the
   document stays, `memory_search` finds it); index lines of the owner's facts you move, never reword.
5. Once a week (`sleep-last` says when you last did): lessons of one command or skill that keep coming back are
   worth a change of its skill — propose it with `document_propose`, the whole new text and why; the owner decides.
   One proposal a skill; none when nothing repeats.
6. Update your lesson `sleep-last`: when this sleep ended (`source: process:<this process>`), and the date of
   the last weekly pass.
7. `memory_overview` gives the numbers: write one `owner_notify` only when something changed, one line: lessons
   written, confirmed, merged, deleted, proposals waiting for the owner — and the repeats of failures from the
   overview.

The result is a short report: what changed (lessons written, confirmed, merged, deleted; proposals with their
source), and the numbers of `memory_overview`; or that nothing needed a change.
