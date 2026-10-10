---
name: dispatch
description: Puts the owner's tasks into the right deck project as records of its tasks, or into a command call, and moves them between projects on request. Use when the owner gives a task, an idea or a bug in a few words, or says "put it into X" or "move it to Y".
---

# Putting tasks where they belong

## Which project

1. `projects_list` gives each project with its description, the first line of its md prompt. Pick by meaning,
   not by words: "the button on the cockpit is off" is a task of the project whose description is the deck code,
   even if the owner never named it.
2. The screen the owner is on is a strong hint: a message written on a project's screen is most likely about
   that project.
3. The owner named a project ("put it into deck2") — that one, even when another looks closer.
4. Two projects fit equally, or none does — ask one question with options, the likeliest first; "a new
   project" is an option only when the task starts a new line of work (then the module-builder skill).

## Record or command

- Something a project already does on request ("run the collection now", "merge it") — call that command.
- A piece of work someone does later — a record of the collection where the project keeps its tasks
  (`manifest_get`: the collection whose description says so, such as `tasks` of a code project), written with
  `records_upsert` by its schema. Title — a short imperative ("Fix the date on the cockpit"); text — what the
  owner said, the screen it was said on, and what you know that helps (links, ids, the record it is about).
  Don't invent requirements; write open questions as a list at the end.
- Several tasks in one message — one record each, in the order said.
- The project keeps no tasks: say so and offer to add a collection of tasks with its screen (the module-builder
  skill); do not put the task anywhere else.

## Saying it

Answer with what you did: "Put it into deck2 as task #41." When you chose the project yourself, add "move it?"
so the owner can say where else. When the owner says "move it to X": write the same task into the tasks of X and
delete the old one with `record_delete`, which you may for what your own turns wrote; the owner can undo it from
the feed. A record you did not write stays: say both numbers with a link to the old one.
