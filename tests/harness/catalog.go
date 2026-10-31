package harness

// ForecastManifest is a module that tracks a price and forecasts its change: a stat of the last price, a table
// of forecasts and a forecast page, each value shown by its format.
const ForecastManifest = `{"version": 13,
  "collections": [
    {"name": "prices", "description": "Prices", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "price": {"type": "number"}, "forecast": {"type": "number"}}, "required": ["at"]}},
    {"name": "forecasts", "description": "Forecasts", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "horizon": {"type": "string"}, "change": {"type": "number"},
        "confidence": {"type": "number"}, "status": {"type": "string"}, "source": {"type": "string"}}, "required": ["at"]}},
    {"name": "attachments", "description": "Files", "key": "title", "display": "title",
      "schema": {"type": "object", "properties": {"title": {"type": "string"}, "file": {}}, "required": ["title"]}}],
  "screens": [
    {"name": "main", "title": "BTC", "blocks": [
      {"type": "stat", "title": "Last price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price",
        "format": "money", "currency": "USD", "tone": "rows[0].price >= 60000 ? 'ok' : 'warn'", "empty": "No prices yet"},
      {"type": "table", "title": "Forecasts", "source": {"collection": "forecasts", "sort": "-at"},
        "columns": [{"label": "Made", "bind": "at", "format": "relative"}, {"label": "Horizon", "bind": "horizon"},
          {"label": "Change", "bind": "change", "format": "percent", "tone": "change < 0 ? 'error' : 'ok'"},
          {"label": "Status", "bind": "status", "format": "tag", "tone": "status == 'missed' ? 'warn' : 'none'"},
          {"label": "Source", "bind": "source", "format": "link"}],
        "actions": [{"label": "Open", "screen": "forecast", "primary": true}], "empty": "No forecasts yet"},
      {"type": "list", "title": "Horizons", "source": {"collection": "forecasts", "sort": "-at"},
        "item": {"title": "horizon", "tone": "status == 'missed' ? 'muted' : ''"}},
      {"type": "list", "title": "Missed", "when": "len(rows) > 0", "source": {"collection": "forecasts", "where": "status == 'missed'"},
        "item": {"title": "horizon"}},
      {"type": "chart", "title": "Price", "source": {"collection": "prices", "sort": "at"}, "x": "at",
        "series": [{"label": "BTC", "y": "price"}, {"label": "Forecast", "y": "forecast"}], "currency": "USD", "format": "money", "empty": "No prices yet"}]},
    {"name": "files", "title": "Files", "blocks": [
      {"type": "media", "title": "Attachments", "source": {"collection": "attachments", "sort": "title"}, "item": {"file": "file", "title": "title"},
        "empty": "No files yet"}]},
    {"name": "projects", "title": "Projects", "blocks": [
      {"type": "chart", "kind": "bar", "title": "Revisions", "source": {"from": "deck.projects_list"}, "x": "name", "series": [{"label": "Revision", "y": "revision"}]}]},
    {"name": "forecast", "title": "Forecast", "record": "forecasts", "blocks": [
      {"type": "card", "fields": [{"label": "Made", "bind": "at", "format": "datetime"}, {"label": "Change", "bind": "change", "format": "percent", "tone": "change < 0 ? 'error' : 'ok'"},
        {"label": "Confidence", "bind": "confidence", "format": "percent"}]},
      {"type": "card", "title": "Still open", "when": "status == 'open'", "fields": [{"label": "Horizon", "bind": "horizon"}]}]}],
  "menu": ["main"]}`
