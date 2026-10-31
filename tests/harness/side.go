package harness

const SideManifest = `{"version": 13, "i18n": {"ru": {"Trades": "Сделки", "Coin": "Монета", "Side": "Сторона", "long": "в рост", "short": "в падение"}},
  "collections": [{"name": "trades", "description": "Trades", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "coin": {"type": "string"}, "side": {"type": "string"}}, "required": ["title"]}}],
  "commands": [],
  "screens": [{"name": "lab", "title": "Trades", "params": {"coin": "string", "side": "string"},
    "side": [
      {"type": "filter", "title": "Coin", "param": "coin", "source": {"collection": "trades"}, "field": "coin"},
      {"type": "filter", "title": "Side", "param": "side", "options": ["long", "short"]},
      {"type": "stat", "title": "Open", "source": {"collection": "trades", "where": "param.coin == nil || coin == param.coin"}},
      {"type": "actions", "actions": [{"label": "All coins", "screen": "lab"}]}],
    "blocks": [{"type": "table", "title": "Trades", "source": {"collection": "trades", "sort": "title",
        "where": "(param.coin == nil || coin == param.coin) && (param.side == nil || side == param.side)"},
      "columns": [{"label": "Title", "bind": "title"}]}]}]}`
