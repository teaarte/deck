---
name: dispatch
description: Puts the owner's tasks into the right deck project as cards, or into a command call, and moves them between projects on request. Use when the owner gives a task, an idea or a bug in a few words, or says "put it into X" or "move it to Y".
---

# Putting tasks where they belong

## Which project

1. `projects_list` gives each project with its description, the first line of its md prompt. Pick by meaning,
   not by words: "the button on the board is off" is a task of the project whose description is the deck code,
   even if the owner never named it.
2. The screen the owner is on is a strong hint: a message written on a project's screen is most likely about
   that project.
3. The owner named a project ("put it into deck2") — that one, even when another looks closer.
4. Two projects fit equally, or none does — ask one question with options, the likeliest first; "a new
   project" is an option only when the task starts a new line of work (then the module-builder skill).

## Card or command

- A piece of work someone does later — a card: `card_create` in that project without a lane, so it lands in the
  inbox lane. Title — a short imperative ("Fix the date on the board card"); text — what the owner said, the
  screen it was said on, and what you know that helps (links, ids, the record it is about). Don't invent
  requirements; write open questions as a list at the end.
- Several tasks in one message — one card each, in the order said; when one needs another first, give it
  `waits` with the earlier card.
- Something a project already does on request ("run the collection now", "merge it") — call that command
  instead of making a card.
- A record of a project is the subject ("this job looks good, prepare it") — the card gets `record`.

## Saying it

Answer with what you did: "Put it into deck2 as #41 (Backlog)." When you chose the project yourself, add
"move it?" so the owner can say where else. When the owner says "move it to X": create the same card in X
(title, text, record), delete the old one with `card_delete`, and say both numbers.
