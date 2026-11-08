Look over deck once a day and tell the owner what is worth doing; do not do it yourself.

1. `projects_list`, then for each enabled project: its processes of the last day (`processes_list` with the
   project, newest first: stop at the first that started before then), the documents its modules keep as summaries
   (`documents_list`; a module names its summary in its md), and what is waiting for the owner (`asks_list`); then
   `health_list`: what is red or paused, such as a model out of credits, pushes that stopped, an error that outlived its
   cause. A process of the day that handed in its result (`submitted_at`) with no record or step after to show for it
   is a finding too.
2. `changes.md` of project master: the lines after the one you read last are what changed in deck for modules; a
   module that needs a change by them is a finding. The line you read last is your lesson `review-changes-read` in
   the owner's memory (project master), with `command: review` and `source: process:<this process>`, by the memory
   skill; after reading, save it with the last line you read.
3. What the owner's memory says they turned down, you do not bring up again.
4. When something is worth it, write one `owner_notify` for the day: at most five findings, the most useful first, each
   a line with what you saw, why it helps and the words the owner can say to you in the chat to have it done.
   Good findings: a command that keeps failing, a module whose summary shows work piling up, what waits for the
   owner for days, a follow-up the owner asked for in a topic and nobody did.
5. When nothing is worth it, write nothing.

The result is one short line: how many findings you wrote, or that there was nothing to tell.
