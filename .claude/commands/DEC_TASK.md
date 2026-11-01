---
description: Take tasks from the deck board "DEC tasks" and do them in this repo
argument-hint: "[all | task id — the oldest task when empty]"
---

The open tasks of this repo are on the deck board "DEC tasks"; deck keeps them in
`~/.deck2/work/projects/15/data/tasks.json`: a list of `{"id", "title", "text"}`, oldest first.
The text is the owner's words, often dictated by voice: read it for what is meant, not word by word.

When `$ARGUMENTS` is `all`, do every open task:

1. Read the file and all its tasks. When the list is empty, say the board has no tasks and stop.
2. Look at the code the tasks touch and choose the order: tasks that others build on first, tasks that change
   the same place together, small and clear ones before big ones. Show the owner the order in a short list.
3. Collect every question the tasks leave open and ask them all at once, before changing anything; a task the
   owner leaves unclear is skipped, not guessed.
4. Do the tasks one by one in that order the usual way: understand the code, make the change, run the tests.
   Finish one, tests passing, before the next; when one cannot be done, say why and go on to the next.
5. At the end list per task in one or two lines what changed, the tasks skipped and why, and remind the owner
   to press "Done" on the board for the finished ones: #<id>, #<id>, …

Otherwise do one task:

1. Read that file. Take the task with id `$ARGUMENTS` when it is given, else the first one.
   When the list is empty, say the board has no tasks and stop.
2. Do the task in this repo the usual way: understand the code, make the change, run the tests. When the task is
   unclear, ask the owner before changing anything.
3. At the end say in two lines what changed, and remind the owner to press "Done" on task #<id> on the board.
