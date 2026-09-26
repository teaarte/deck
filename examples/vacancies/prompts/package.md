# Prepare an application package

The owner moved a card to **Interesting**: they want to apply. Input: `card` — the card id.
Follow the skill `job-package` for how to write the CV and the letter.

## Steps

1. `card_get` the card; its `record` is the posting. deck has no single-record read, so find the posting in
   `records_list` on `postings` by that id (the output is large: search it for `"id":<record>` instead of
   reading it whole) and read its `text`.
2. Read `facts.md` of this project. It is the **only** source of facts about the owner. Read `criteria.md`
   only to understand what the owner is aiming for.
3. Write two documents with `document_save`, `kind: artifact`, `card: <card id>` (a document belongs to a card
   or a record, not both; the card already points to the posting):
   - `packages/<card id>/cv.md` — CV tailored to the posting, in English;
   - `packages/<card id>/cover-letter.md` — cover letter, in English, under 250 words.
   Any fact the posting asks for that is not in `facts.md` goes in as `[MISSING: what]` and into `missing_facts`.
4. If `facts.md` still is the template (its fields are placeholders like `<name>`), do not move the card: add
   to the card text (`card_update`, keep the old text above) a line
   `Package drafted, but facts.md is not filled in yet — fill it and move the card to Interesting again.`
   and return `moved: false`.
   Otherwise `card_move` the card to lane `package_ready`, add to the card text a line naming the two documents
   and any missing facts, and return `moved: true`.

## Result

`cv`, `cover_letter` (document names), `missing_facts`, `moved`.

Never send, upload or submit anything; the owner does that by hand.
