---
name: interview
description: Interviews the owner about a card until it can be built without guessing, writes the spec into the card and, after the owner's yes, splits it into cards that wait for each other. Use when a topic is about a card and the owner asks for an interview, a spec or a plan, or a task is too vague to start.
---

# Interview on a card

The topic is about one card (deck says which). The goal: the card's text becomes a spec someone can build from,
and big work becomes several cards in the right order.

## 1. Read first

`card_get` the card, then what it points to: its record, the project's md (`document_get prompt.md`), the board
(`cards_list`) and the project's memory. Do not ask what these already answer.

## 2. Ask, one question at a time

With your question tool: one question, 2–4 options, the first one your recommendation and why in its
description; the owner may also answer in their own words. Ask about the decisions that change what gets
built, in this order, and skip what does not apply:

- the outcome: what the owner wants to see when it is done, and how they will check it;
- the boundaries: what is in and what is explicitly out;
- the inputs and outputs: data, screens, commands, files, people;
- the edge cases that matter: empty, many, failure, a second time;
- the constraints: rights, money, privacy (no network), time;
- the order: what must come first.

Stop when you could write the spec without guessing. Seven questions is a lot; three can be enough.

## 3. Write the spec into the card

`card_update` with the text: the owner's original text first, then `## Spec` with short sections — Goal,
Done when, In, Out, Details (the decisions, each with the owner's choice), Open (what is still undecided, if
anything). Plain words, no invented requirements. Say in the topic that the spec is in the card.

## 4. Split, after a yes

When the work is more than one sitting, show the split in the topic as a numbered list: a title per card and,
for each, which earlier ones it waits for. Ask "Shall I create these cards?". Only after the owner says yes in
the topic, create them with `card_create` in the card's project, in order, each with `waits` on the cards it
depends on (the new ids), and link back to the spec card in their text. Do not use `suggest` for this: the
owner already said yes. Then say the numbers.

## Without a card

A new line of work ("I want a weekly video", "watch the price of X") is the same interview by the
module-builder skill, ending in a draft module instead of a card spec.
