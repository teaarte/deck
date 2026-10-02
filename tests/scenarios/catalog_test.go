package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

// forecastManifest is a module that tracks a price and forecasts its change: a stat of the last price, a table
// of forecasts and a forecast page, each value shown by its format.
const forecastManifest = `{"version": 5,
  "collections": [
    {"name": "prices", "description": "Prices", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "price": {"type": "number"}}, "required": ["at"]}},
    {"name": "forecasts", "description": "Forecasts", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "horizon": {"type": "string"}, "change": {"type": "number"},
        "confidence": {"type": "number"}, "status": {"type": "string"}, "source": {"type": "string"}}, "required": ["at"]}}],
  "screens": [
    {"name": "main", "title": "BTC", "blocks": [
      {"type": "stat", "title": "Last price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price",
        "format": "money", "currency": "USD"},
      {"type": "table", "title": "Forecasts", "source": {"collection": "forecasts", "sort": "-at"},
        "columns": [{"label": "Made", "bind": "at", "format": "relative"}, {"label": "Horizon", "bind": "horizon"},
          {"label": "Change", "bind": "change", "format": "percent"}, {"label": "Status", "bind": "status", "format": "tag"},
          {"label": "Source", "bind": "source", "format": "link"}],
        "actions": [{"label": "Open", "screen": "forecast"}]}]},
    {"name": "forecast", "title": "Forecast", "record": "forecasts", "blocks": [
      {"type": "card", "fields": [{"label": "Made", "bind": "at", "format": "datetime"}, {"label": "Change", "bind": "change", "format": "percent"},
        {"label": "Confidence", "bind": "confidence", "format": "percent"}]}]}],
  "menu": ["main"]}`

func (e *Env) seedForecasts() {
	e.T.Helper()
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices",
		"records": jsonl(map[string]any{"at": "2026-10-01T10:00:00Z", "price": 61000.5}, map[string]any{"at": "2026-10-02T10:00:00Z", "price": 62340.25})}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "forecasts",
		"records": jsonl(map[string]any{"at": "2026-10-02T09:00:00Z", "horizon": "7d", "change": 0.042, "confidence": 0.6, "status": "open",
			"source": "https://example.com/a"})}, nil)
}

// TestBlocksShowValuesByTheirFormat: a column, a card field and a stat carry their format to the client and keep
// the value raw; a format the catalog lacks, money without its currency and a currency without money are refused.
func TestBlocksShowValuesByTheirFormat(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("btc", forecastManifest)
	e.seedForecasts()

	msgs := e.screenMessages("btc", "main", nil)
	check.messages(t, msgs)
	comps := string(msgs[1]["updateComponents"])
	for _, want := range []string{`"component":"stat","currency":"USD","data":{"path":"/b0"},"format":"money"`,
		`"columns":[{"format":"relative","label":"Made"},{"label":"Horizon"},{"format":"percent","label":"Change"},{"format":"tag","label":"Status"},{"format":"link","label":"Source"}]`} {
		if !strings.Contains(comps, want) {
			t.Fatalf("the format goes with the block, not into its values: %s lacks %s", comps, want)
		}
	}
	data := e.screenData("btc", "main", nil)
	if v := string(data["b0"]["value"]); v != "62340.25" {
		t.Fatalf("a stat keeps its value raw: %s", v)
	}
	rows := blockData[[]screenRow](t, data["b1"]["rows"])
	if len(rows) != 1 || rows[0].Cells[2] != 0.042 || rows[0].Cells[0] != "2026-10-02T09:00:00Z" {
		t.Fatalf("cells keep their values raw: %+v", rows)
	}
	page := e.screenMessages("btc", "forecast", map[string]any{"record": rows[0].ID})
	check.messages(t, page)
	card := blockData[struct {
		Fields []map[string]any
	}](t, e.screenData("btc", "forecast", map[string]any{"record": rows[0].ID})["b0"]["value"])
	if len(card.Fields) != 3 || card.Fields[1]["format"] != "percent" || card.Fields[1]["value"] != 0.042 || card.Fields[0]["format"] != "datetime" {
		t.Fatalf("a card field carries its format: %+v", card.Fields)
	}

	for _, tc := range []struct{ from, to, want string }{
		{`"format": "percent"}, {"label": "Status"`, `"format": "percentage"}, {"label": "Status"`, `value must be one of 'number', 'percent', 'money'`},
		{`"format": "money", "currency": "USD"`, `"format": "money"`, `format money needs currency`},
		{`"format": "money", "currency": "USD"`, `"format": "money", "currency": "dollars"`, `does not match pattern`},
		{`{"label": "Change", "bind": "change", "format": "percent"}, {"label": "Status"`,
			`{"label": "Change", "bind": "change", "format": "percent", "currency": "EUR"}, {"label": "Status"`, `column "Change": currency goes with format money`},
		{`{"label": "Confidence", "bind": "confidence", "format": "percent"}`, `{"label": "Confidence", "bind": "confidence", "format": "money"}`,
			`field "Confidence": format money needs currency`},
	} {
		if strings.Count(forecastManifest, tc.from) != 1 {
			t.Fatalf("fixture has %s once", tc.from)
		}
		code, out := e.call("deck.manifest_save", map[string]any{"project": "btc", "manifest": strings.Replace(forecastManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
	var format json.RawMessage
	e.must("deck.manifest_format", nil, &format)
	if !strings.Contains(string(format), "0.42 is 42 %") {
		t.Fatalf("the model reads the formats in manifest_format: %.300s", format)
	}
}
