package harness

// WidgetsOfWork is a module whose views mix numbers, lists with buttons, a title too long for one cell and two lists
// with their titles, as the owner's modules do.
const WidgetsOfWork = `{"version": 13,
  "i18n": {"ru": {"Laboratory of setups": "Лаборатория сетапов", "Price": "Цена BTC", "Open": "Открыть", "Needs you": "Ждёт вас", "New": "Новое"}},
  "title": "Laboratory of setups",
  "collections": [{"name": "items", "description": "An item.", "key": "name", "display": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "n": {"type": "integer"}, "at": {"type": "string"}}, "required": ["name"]}}],
  "screens": [{"name": "item", "title": "Item", "record": "items", "blocks": [{"type": "card", "fields": [{"label": "Name", "bind": "name"}]}]}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Price", "description": "The last price of the hour, as the exchange gave it", "icon": "currency-bitcoin", "source": {"collection": "items", "limit": 1}, "format": "money", "currency": "USD", "value": "80484.37"}]},
    {"min": "1x2", "blocks": [{"type": "list", "focus": true, "title": "Needs you", "source": {"collection": "items", "where": "n > 100"}, "item": {"title": "name"}},
      {"type": "stat", "title": "Price", "source": {"collection": "items", "limit": 1}, "format": "money", "currency": "USD", "value": "80484.37"}]},
    {"min": "2x1", "blocks": [
      {"type": "stat", "title": "Price", "icon": "currency-bitcoin", "source": {"collection": "items", "limit": 1}, "format": "money", "currency": "USD", "value": "80484.37"},
      {"type": "list", "source": {"collection": "items", "sort": "-n"}, "item": {"title": "name", "meta": "'step ' + string(n)", "since": "at", "icon": "'sparkles'"},
        "actions": [{"label": "Open", "screen": "item", "primary": true}]}]},
    {"min": "2x2", "blocks": [
      {"type": "list", "title": "Needs you", "source": {"collection": "items", "sort": "-n", "limit": 2}, "item": {"title": "name", "meta": "'step ' + string(n)", "icon": "'hand-finger'"},
        "actions": [{"label": "Open", "screen": "item", "primary": true}]},
      {"type": "list", "title": "New", "source": {"collection": "items", "sort": "n"}, "item": {"title": "name", "meta": "'step ' + string(n)", "since": "at", "icon": "'sparkles'"}}]}
  ]}`
