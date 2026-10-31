---
name: themes
description: Makes, changes, checks and deletes the themes of deck — colors of both schemes, fonts, radii, the look of buttons, cards, the cards of modules, islands, fields, badges, notifications, menus, dialogs, tabs and chat bubbles, a little CSS, the mood of pictures and the pictures of the theme. Use when the owner asks for a new look of deck, a theme, colors, fonts, a background or a style for covers.
---

# Themes of deck

A theme is the look of the whole deck: a JSON document checked before it is saved. `deck.theme_format_get` gives its
schema with the catalog — the tokens (canvas, island, block, text, dimmed, …), the variables a value may read and
the classes CSS may name. deck ships juce alone; `deck.themes_list` names it and the owner's. Other looks are
examples in this skill's folder `examples/` — default, glass (frosted islands), neon (dark only), paper and pixel:
read one with `document_get` (`skills/themes/examples/<name>.json` of the master) and save it, changed as the owner
asks, as a theme of its own name.

## Making or changing one

1. Start from the closest theme or example: `deck.theme_get` with its name gives its `text`. Save under its name to
   put the owner's over juce, under a new name for a new one.
2. Write the tokens of both schemes (`tokens` for both, `light`, `dark` over them). Text and dimmed need 4.5:1
   on canvas, body, island and block with every accent: give those tokens as #hex, rgb() or color-mix().
3. `mood` is the style of pictures in this theme in one or two sentences — the covers of modules are drawn by it,
   each kept for its theme: `deck.covers_draw` without a module draws those this theme lacks.
4. The cards of modules have their own tokens: `cockpit` (the cockpit's card; the accent by default), `tile` (a
   card without a cover), `tile-shade`, `tile-text`, `tile-veil` (under each line of Today over its picture; empty
   for the color opposite to `tile-text`), `tile-icon`, `tile-shadow`, `tile-lift`, and of their grid
   `tile-min`, `tile-height`, `tile-gap` (one cell: a card stands on whole cells); classes `.widget` (the view of a
   module in its card), `.left-out` (the mark of the blocks a card has no room for) and `.placing` (a card while the
   owner places the tiles); `meadow` is the ground at the foot of the page, `toast` and `toast-text` the note with
   Undo. Text on the cockpit and on a card without a cover needs 4.5:1 under the last stop of its shade.
5. CSS only when tokens and parts are not enough, and only by the classes of the catalog: no url(), no
   @import, no selectors of the page. A card of a module is `.tile` (`.today` the cockpit's, `[data-cover]` one
   with a picture), a card of a board `.board-card`; `.card` names both and stays for older themes. `.places` and
   `.place` are the tabs of the screens under the head, `.nav` the tabs at the foot of a phone, `.nav-item` one.
   The chat: `.message` (`.bubble` the owner's, `.chat-link` a link of a reply), `.turn` with `.turn-head`,
   `.turn-step` and `.turn-log`, `.suggest`, `.chat-ask` (a yes it waits for), `.question`, `.cut` (a reply that
   broke off), `.told` (deck's line of how a work from the topic went), `.composer` with `.attached` (a file put
   in before sending) and `.hearing` (the voice being recorded). The side panel of a module's screen is `.side` (`.side-sheet` on a phone, `side` and
   `side-width` its tokens); `.empty` the words of a block with nothing, `.pending` what loads, `.sheet` a sheet
   from the edge; parts `widget`, `empty` and `pending` style them without CSS, and `placing` is the color of the
   free cells while the owner places the tiles. Radius `0` squares all of them already.
6. `deck.theme_check` with the text names every problem; fix them all, then `deck.theme_save`. Its answer
   gives two preview addresses, light and dark: give them to the owner as links. Look at it before the owner
   does: `screen_capture` with `theme` and `scheme` draws a screen in it (the tiles `#/`, Today `#/deck/cockpit`, a
   module) on a computer and a phone; read the picture at `path`.
7. Only the owner puts a theme on: tell them it is in Look, the place beside Files.

## Pictures of a theme

A theme may have pictures, each for the light and the dark scheme, set apart from its text with
`deck.theme_image_set` (theme, slot, scheme, the file as `results/<name>` of an image call): `cockpit`, the scene of the
card of Today under its words (calm on the left, room for Deki on the right); `backdrop`, one wide picture behind
the page; or `pattern`, a seamless square tile repeated behind it, `pattern-size` wide. Keep them quiet: they stay
behind everything. Draw them with `deck.model_call` of kind image in the theme's `mood`; the owner's button
"Draw the background" in Look asks for them (`deck.theme_draw`). `deck.theme_check` checks the words of
Today over the cockpit by its main color; look at the tiles with `screen_capture` after.

## Deleting

`deck.theme_delete` removes the owner's theme and its pictures; over juce, juce is back. A deleted theme in use
gives way to juce.
