---
name: chat-blocks
description: Shows the owner one block in a reply of the chat — options to pick, a form with sliders, a table, a chart or a card — with deck.chat_block_show. Use when the owner answers better by a press than by words, or rows or a trend read better drawn than told.
---

# Blocks in the chat

A reply shows at most one block, under its text. Call `deck.chat_block_show` with `block` as JSON, then end the turn
with one to three lines that go over it: what you ask or what the block shows. The owner's answer to options or a
form comes as the owner's next message of the topic, its fields as `label: value` lines; act on it in that turn.

## Which block

- **choice** — 2–6 short answers the owner picks one of: a yes or no with a third way, a plan out of a few, which
  module. A label of a few words; a description when the choice is not plain from it.
- **form** — two or more values, or a number in a range: a price, a term, a count, a time of day. A number with
  `minimum` and `maximum` is a slider with the exact value beside it; `multipleOf` is its step, a few `examples` are
  marks the slider is pulled to. A choice of words is a string `enum`; yes or no a `boolean`. Fill what you know in
  `values`. No files: the owner attaches them to a message.
- **table** — rows to compare, at most 50, the main columns first.
- **chart** — a trend over time or a comparison by bars: `x` and a series of values for each x.
- **card** — one thing in fields: a label and a value each, `markdown` for a value with links or lists.
- Text alone when the answer is words, or a block would only repeat the text. More than one block, or a screen the
  owner comes back to, is a link to a screen of a module instead.
- A question that must wait for the owner in "waiting for you", out of the topic, goes through the question tool.

## Shapes

```json
{"type": "choice", "title": "Way", "options": [{"label": "Train", "description": "slow, cheap"}, {"label": "Plane"}]}
{"type": "form", "title": "Offer", "submit": "Send", "values": {"price": 100},
 "input": {"type": "object", "required": ["price"], "properties": {
   "price": {"type": "integer", "title": "Price, $", "minimum": 0, "maximum": 1000, "multipleOf": 10, "examples": [100, 200, 500]},
   "rush": {"type": "boolean", "title": "Rush"}}}}
{"type": "table", "title": "Models", "columns": [{"label": "Model"}, {"label": "Price", "format": "money", "currency": "USD"}],
 "rows": [["qwen3", 0], ["gpt-oss", 0.18]]}
{"type": "chart", "title": "Replies", "kind": "bar", "x": ["Mon", "Tue"], "series": [{"label": "qwen3", "values": [4, 7]}]}
{"type": "card", "title": "Order", "fields": [{"label": "Total", "value": 120, "format": "money", "currency": "USD"}]}
```

Limits: a choice of 2–6 options, a label up to 60 characters; a form of 1–8 fields; a table of 1–8 columns and up
to 50 rows; a chart of up to 500 x and 5 series; a card of up to 12 fields; a title up to 120 characters. A wrong
block is refused with why; mend it and call again: a second block of the same turn takes the place of the first
while the owner has not answered it. The answer to a choice comes as its title and the label picked.
