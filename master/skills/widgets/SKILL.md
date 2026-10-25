---
name: widgets
description: Makes the views of the card of a module on the tiles of deck — what the card shows on each size, from stat, list and chart blocks. Use when the owner asks Deki to prepare a view for a size of a card (deck.widget_draw), or asks what a card of a module should show.
---

# Widgets of a module

A card on the tiles of deck shows the largest view of `widgets` of its module's manifest that it fits; without one it
is the cover with the title. A view is a small screen: `{"min": "2x1", "blocks": [...]}`, where `min` is the least
size it fits, columns by rows of cells, and `blocks` are one to four `stat`, `list` and `chart` blocks read like on a
screen (sources, `where`, `sort`, expressions, buttons). A list without `limit` shows the lines the card holds, three
a row; a chart takes the width it gets. A press on the card opens the module; a button inside presses as on a screen.

## What to show on what size

- `1x1`: one number or one word, a `stat`: what waits, what is new today, the last value.
- `2x1`: the stat and one line, or a list of the two most pressing items.
- `2x2` and up: the list of what needs the owner first, then what is new; a chart only of a trend the owner follows.
- Wider: a chart beside a list. Taller: a longer list, not more kinds of blocks.

Do not cram: one question per view — what needs me, what is new, how it goes. Say it in the module's words with its
`i18n`; no ids, command names or raw data in titles.

## How

1. Read the module: `manifest_get`, its collections and the records the view would show.
2. Write the view for the size the owner asked (`min` is that size) and keep the views of other sizes.
3. `manifest_check` it, then `manifest_save` it; a view widens no right, so it saves at once.
4. Tell the owner in one sentence what the card shows now.
