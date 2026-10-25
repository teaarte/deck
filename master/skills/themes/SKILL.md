---
name: themes
description: Makes, changes, checks and deletes the themes of deck — colors of both schemes, fonts, radii, the look of buttons, cards, the cards of modules, islands, fields, badges, notifications, menus, dialogs, tabs and chat bubbles, a little CSS and the mood of pictures. Use when the owner asks for a new look of deck, a theme, colors, fonts or a style for covers.
---

# Themes of deck

A theme is the look of the whole deck: a JSON document checked before it is saved. `deck.theme_format` gives its
schema with the catalog — the tokens (canvas, island, block, text, dimmed, …), the variables a value may read and
the classes CSS may name. deck ships juce, default, glass, pixel, neon and paper; `deck.themes_list` names them
and the owner's.

## Making or changing one

1. Start from the closest theme: `deck.theme` with its name gives its `text`. Save under its name to put the
   owner's over a shipped one, under a new name for a new one.
2. Write the tokens of both schemes (`tokens` for both, `light`, `dark` over them). Text and dimmed need 4.5:1
   on canvas, body, island and block with every accent: give those tokens as #hex, rgb() or color-mix().
3. `mood` is the style of pictures in this theme in one or two sentences — the covers of modules are drawn by it,
   each kept for its theme: `deck.covers_draw` without a module draws those this theme lacks.
4. The cards of modules have their own tokens: `cockpit` (the cockpit's card; the accent by default), `tile` (a
   card without a cover), `tile-shade`, `tile-text`, `tile-icon`, `tile-shadow`, `tile-lift`, and of their grid
   `tile-min`, `tile-height`, `tile-gap` (one cell: a card stands on whole cells); classes `.widget` (the view of a
   module in its card) and `.placing` (a card while the owner places the tiles); `meadow` is the ground under the sky, `toast` and `toast-text` the note
   with Undo. Text on the cockpit and on a card without a cover needs 4.5:1 under the last stop of its shade.
5. CSS only when tokens and parts are not enough, and only by the classes of the catalog: no url(), no
   @import, no selectors of the page. A card of a module is `.tile` (`.today` the cockpit's, `[data-cover]` one
   with a picture), a card of a board `.board-card`; `.card` names both and stays for older themes. `.places` and
   `.place` are the tabs of the screens under the head, `.nav` the tabs at the foot of a phone, `.nav-item` one.
   The chat: `.message` (`.bubble` the owner's, `.chat-link` a link of a reply), `.turn` with `.turn-head`,
   `.turn-step` and `.turn-log`, `.suggest`, `.chat-ask` (a yes it waits for), `.question`, `.cut` (a reply that
   broke off), `.composer` and `.hearing` (the voice being recorded). Radius `0` squares all of them already.
6. `deck.theme_check` with the text names every problem; fix them all, then `deck.theme_save`. Its answer
   gives two preview addresses, light and dark: give them to the owner as links, and `unreached`, the parts of
   deck no theme changes yet: do not chase them with CSS.
7. Only the owner puts a theme on: tell them it is in Settings → Look.

## Deleting

`deck.theme_delete` removes the owner's theme; over a shipped one, the shipped one is back. A deleted theme in
use gives way to juce.
