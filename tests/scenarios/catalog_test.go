package scenarios

import (
	"encoding/json"
	"fmt"
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
        "item": {"title": "horizon"}}]},
    {"name": "forecast", "title": "Forecast", "record": "forecasts", "blocks": [
      {"type": "card", "fields": [{"label": "Made", "bind": "at", "format": "datetime"}, {"label": "Change", "bind": "change", "format": "percent", "tone": "change < 0 ? 'error' : 'ok'"},
        {"label": "Confidence", "bind": "confidence", "format": "percent"}]},
      {"type": "card", "title": "Still open", "when": "status == 'open'", "fields": [{"label": "Horizon", "bind": "horizon"}]}]}],
  "menu": ["main"]}`

func (e *Env) seedForecasts() {
	e.T.Helper()
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices",
		"records": jsonl(map[string]any{"at": "2026-10-01T10:00:00Z", "price": 61000.5}, map[string]any{"at": "2026-10-02T10:00:00Z", "price": 62340.25})}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "forecasts",
		"records": jsonl(map[string]any{"at": "2026-10-02T09:00:00Z", "horizon": "7d", "change": 0.042, "confidence": 0.6, "status": "open",
			"source": "https://example.com/a"},
			map[string]any{"at": "2026-10-01T09:00:00Z", "horizon": "1d", "change": -0.01, "confidence": 0.3, "status": "missed"})}, nil)
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
	if len(rows) != 2 || rows[0].Cells[2] != 0.042 || rows[0].Cells[0] != "2026-10-02T09:00:00Z" {
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
		{`"format": "relative"}`, `"format": "relatively"}`, `value must be one of 'number', 'percent', 'money'`},
		{`"format": "money", "currency": "USD"`, `"format": "money"`, `format money needs currency`},
		{`"format": "money", "currency": "USD"`, `"format": "money", "currency": "dollars"`, `does not match pattern`},
		{`{"label": "Made", "bind": "at", "format": "relative"}`, `{"label": "Made", "bind": "at", "format": "relative", "currency": "EUR"}`,
			`column "Made": currency goes with format money`},
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

// TestBlocksTakeTheToneOfTheirValues: a column, a list line, a card field and a stat take a tone by an expression
// over what they show; a value that is not a tone is none, and a tone over an unknown field is refused.
func TestBlocksTakeTheToneOfTheirValues(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("btc", forecastManifest)
	e.seedForecasts()
	check.messages(t, e.screenMessages("btc", "main", nil))
	data := e.screenData("btc", "main", nil)
	if tone := string(data["b0"]["tone"]); tone != `"ok"` {
		t.Fatalf("a stat takes its tone over rows: %s", tone)
	}
	var rows []struct {
		ID    any   `json:"id"`
		Tones []any `json:"tones"`
	}
	_ = json.Unmarshal(data["b1"]["rows"], &rows)
	if len(rows) != 2 || fmt.Sprint(rows[0].Tones) != "[<nil> <nil> ok <nil> <nil>]" || fmt.Sprint(rows[1].Tones) != "[<nil> <nil> error warn <nil>]" {
		t.Fatalf("a cell takes the tone of its column, parallel to the cells; none is null: %+v", rows)
	}
	var lines []map[string]any
	_ = json.Unmarshal(data["b2"]["items"], &lines)
	if len(lines) != 2 || lines[0]["tone"] != nil || lines[1]["tone"] != "muted" {
		t.Fatalf("a list line takes its tone; an empty one is none: %+v", lines)
	}
	page := e.screenMessages("btc", "forecast", map[string]any{"record": rows[1].ID})
	check.messages(t, page)
	card := blockData[struct{ Fields []map[string]any }](t, e.screenData("btc", "forecast", map[string]any{"record": rows[1].ID})["b0"]["value"])
	if card.Fields[1]["tone"] != "error" || card.Fields[0]["tone"] != nil {
		t.Fatalf("a card field takes its tone over the record: %+v", card.Fields)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"tone": "change < 0 ? 'error' : 'ok'"},
          {"label": "Status"`, `"tone": "chnage < 0 ? 'error' : 'ok'"},
          {"label": "Status"`, `column "Change" tone: unknown field "chnage"`},
		{`"tone": "rows[0].price >= 60000 ? 'ok' : 'warn'"`, `"tone": "price >= 60000 ? 'ok' : 'warn'"`, `tone: unknown field "price"`},
		{`"tone": "status == 'missed' ? 'muted' : ''"`, `"tone": "state == 'missed' ? 'muted' : ''"`, `item: unknown field "state"`},
	} {
		if strings.Count(forecastManifest, tc.from) != 1 {
			t.Fatalf("fixture has %s once", tc.from)
		}
		code, out := e.call("deck.manifest_save", map[string]any{"project": "btc", "manifest": strings.Replace(forecastManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestABlockWithoutItemsSaysSoInItsOwnWords: a block with empty shows its text when its source has none, and a
// stat with it does not evaluate its value over no rows; a block without it is empty in the client's words.
func TestABlockWithoutItemsSaysSoInItsOwnWords(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("btc", forecastManifest)
	check.messages(t, e.screenMessages("btc", "main", nil))
	data := e.screenData("btc", "main", nil)
	for id, want := range map[string]string{"b0": "No prices yet", "b1": "No forecasts yet", "b2": ""} {
		var got struct{ State, Empty, Error string }
		raw, _ := json.Marshal(data[id])
		_ = json.Unmarshal(raw, &got)
		if got.State != "empty" || got.Empty != want || got.Error != "" {
			t.Fatalf("block %s without items is empty with %q: %+v", id, want, got)
		}
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"empty": "No prices yet"`, `"empty": ""`, `minLength`},
		{`{"label": "Confidence", "bind": "confidence", "format": "percent"}]}`, `{"label": "Confidence", "bind": "confidence", "format": "percent"}], "empty": "None"}`,
			`additional properties 'empty' not allowed`},
	} {
		if strings.Count(forecastManifest, tc.from) != 1 {
			t.Fatalf("fixture has %s once", tc.from)
		}
		code, out := e.call("deck.manifest_save", map[string]any{"project": "btc", "manifest": strings.Replace(forecastManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestABlockIsOnTheScreenOnlyWhereItsWhenHolds: a block whose when does not hold over its rows or the record of
// the page is not in the screen at all; an event its when reads resolves the screen whole.
func TestABlockIsOnTheScreenOnlyWhereItsWhenHolds(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("btc", forecastManifest)
	blocks := func(screen string, extra map[string]any) (string, map[string]map[string]json.RawMessage) {
		msgs := e.screenMessages("btc", screen, extra)
		check.messages(t, msgs)
		return string(msgs[1]["updateComponents"]), e.screenData("btc", screen, extra)
	}
	comps, data := blocks("main", nil)
	if strings.Contains(comps, `"title":"Missed"`) || data["b3"] != nil || !strings.Contains(comps, `"children":["b0","b1","b2"]`) {
		t.Fatalf("a block whose when does not hold over its rows is not on the screen: %s %v", comps, data["b3"])
	}
	e.seedForecasts()
	comps, data = blocks("main", nil)
	if !strings.Contains(comps, `"title":"Missed"`) || len(blockData[[]listItem](t, data["b3"]["items"])) != 1 {
		t.Fatalf("it is there once its when holds: %s", comps)
	}
	if msgs := e.screenMessages("btc", "main", map[string]any{"topic": "collection"}); msgs[0]["createSurface"] == nil {
		t.Fatalf("an event the when reads resolves the screen whole: %v", msgs[0])
	}
	if msgs := e.screenMessages("btc", "main", map[string]any{"topic": "asks"}); len(msgs) > 0 && msgs[0]["createSurface"] != nil {
		t.Fatalf("an event no when reads resolves only its blocks: %v", msgs[0])
	}
	var recs []struct {
		ID   int64
		Data struct{ Status string }
	}
	e.must("deck.records_list", map[string]any{"project": "btc", "collection": "forecasts"}, &recs)
	if len(recs) != 2 || recs[0].Data.Status == recs[1].Data.Status {
		t.Fatalf("one forecast is open, one missed: %+v", recs)
	}
	for _, rec := range recs {
		comps, _ := blocks("forecast", map[string]any{"record": rec.ID})
		if strings.Contains(comps, `"title":"Still open"`) != (rec.Data.Status == "open") {
			t.Fatalf("a block reads the record of its page in its when (%s): %s", rec.Data.Status, comps)
		}
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"when": "len(rows) > 0"`, `"when": "status == 'missed'"`, `when: unknown field "status"`},
		{`"when": "status == 'open'"`, `"when": "len(rows) > 0"`, `when: unknown field "rows"`},
	} {
		if strings.Count(forecastManifest, tc.from) != 1 {
			t.Fatalf("fixture has %s once", tc.from)
		}
		code, out := e.call("deck.manifest_save", map[string]any{"project": "btc", "manifest": strings.Replace(forecastManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}
