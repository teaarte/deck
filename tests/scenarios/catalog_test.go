package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"deck2/internal/provider"
)

// forecastManifest is a module that tracks a price and forecasts its change: a stat of the last price, a table
// of forecasts and a forecast page, each value shown by its format.
const forecastManifest = `{"version": 9,
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

func (e *Env) seedForecasts() {
	e.T.Helper()
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices",
		"records": jsonl(map[string]any{"at": "2026-10-01T10:00:00Z", "price": 61000.5}, map[string]any{"at": "2026-10-02T10:00:00Z", "price": 62340.25, "forecast": 63000})}, nil)
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
		{`"format": "relative"}`, `"format": "relatively"}`, `value must be one of 'number', 'compact', 'percent', 'money'`},
		{`"format": "money", "currency": "USD"`, `"format": "money"`, `format money needs currency`},
		{`"format": "money", "currency": "USD"`, `"format": "money", "currency": "dollars"`, `does not match pattern`},
		{`"format": "money", "currency": "USD"`, `"format": "money", "currency": "ZZZ"`, `currency "ZZZ" is not an ISO 4217 code`},
		{`{"label": "Confidence", "bind": "confidence", "format": "percent"}`, `{"label": "Confidence", "bind": "confidence", "format": "percent", "markdown": true}`,
			`a markdown field shows its text as markdown and takes no format or tone`},
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
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "forecasts", "records": jsonl(map[string]any{"at": "2026-09-01T09:00:00Z"})}, nil)
	if got := e.screenData("btc", "main", nil)["b1"]; string(got["state"]) != `"ready"` || len(blockData[[]screenRow](t, got["rows"])) != 3 {
		t.Fatalf("a tone that fails over a row, such as change < 0 without change, is no tone: %s %s", got["state"], got["error"])
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
		{`"empty": "No forecasts yet"`, `"empty": ""`, `minLength`},
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
	if strings.Contains(comps, `"title":"Missed"`) || data["b3"] != nil || !strings.Contains(comps, `"children":["b0","b1","b2","b4"]`) {
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

// TestAChartDrawsTheItemsOfItsSourceAsSeries: a chart of a collection gives x and the values of each series in the
// order of the sort, a gap where a value is missing, at most the last 500 points; a chart of a deck command too.
func TestAChartDrawsTheItemsOfItsSourceAsSeries(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("btc", forecastManifest)
	if got := e.screenData("btc", "main", nil)["b4"]; string(got["state"]) != `"empty"` || string(got["empty"]) != `"No prices yet"` {
		t.Fatalf("a chart without points says so: %s", got["state"])
	}
	e.seedForecasts()
	msgs := e.screenMessages("btc", "main", nil)
	check.messages(t, msgs)
	if comps := string(msgs[1]["updateComponents"]); !strings.Contains(comps, `"component":"chart","currency":"USD","data":{"path":"/b4"},"format":"money","id":"b4","kind":"line"`) {
		t.Fatalf("a chart carries its kind and format: %s", comps)
	}
	type chartV struct {
		X      []any
		Series []struct {
			Label  string
			Values []any
		}
	}
	v := blockData[chartV](t, e.screenData("btc", "main", nil)["b4"]["value"])
	if fmt.Sprint(v.X) != "[2026-10-01T10:00:00Z 2026-10-02T10:00:00Z]" || len(v.Series) != 2 || v.Series[0].Label != "BTC" ||
		fmt.Sprint(v.Series[0].Values) != "[61000.5 62340.25]" || fmt.Sprint(v.Series[1].Values) != "[<nil> 63000]" {
		t.Fatalf("a chart gives x and its series in the order of the sort, null for a gap: %+v", v)
	}
	var many []map[string]any
	for i := range 600 {
		many = append(many, map[string]any{"at": fmt.Sprintf("2026-%02d-%02dT%02d:00:00Z", 1+i/240, 1+i/24%10, i%24), "price": float64(i)})
	}
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices", "records": jsonl(many...)}, nil)
	v = blockData[chartV](t, e.screenData("btc", "main", nil)["b4"]["value"])
	if len(v.X) != 500 || len(v.Series[0].Values) != 500 || v.X[499] != "2026-10-02T10:00:00Z" {
		t.Fatalf("a chart draws the last 500 points: %d, last %v", len(v.X), v.X[len(v.X)-1])
	}
	projects := e.screenMessages("btc", "projects", nil)
	check.messages(t, projects)
	v = blockData[chartV](t, e.screenData("btc", "projects", nil)["b0"]["value"])
	if len(v.X) == 0 || !slices.Contains(v.X, any("btc")) || len(v.Series[0].Values) != len(v.X) || v.Series[0].Values[0] == nil {
		t.Fatalf("a chart reads a deck command: %+v", v)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"x": "at",`, `"x": "time",`, `x: unknown field "time"`},
		{`{"label": "Forecast", "y": "forecast"}`, `{"label": "Forecast", "y": "guess"}`, `series "Forecast": unknown field "guess"`},
		{`"kind": "bar"`, `"kind": "pie"`, `value must be one of 'line', 'bar', 'area'`},
		{`"series": [{"label": "Revision", "y": "revision"}]`, `"series": []`, `minItems`},
		{`"x": "name", `, ``, `missing property 'x'`},
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

// TestMediaShowsTheFilesOfItsProjectOnly: a media line names a stored file of the project or a file of its data
// folder, given at its url; a file of another project or out of the data folder is an error of its line.
func TestMediaShowsTheFilesOfItsProjectOnly(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	id := e.createProject("btc", forecastManifest)
	e.createProject("other", forecastManifest)
	if got := e.screenData("btc", "files", nil)["b0"]; string(got["empty"]) != `"No files yet"` {
		t.Fatalf("media without files says so: %s", got["empty"])
	}
	png := "\x89PNG\r\n\x1a\nfake"
	mine := e.upload("btc", "scan.png", "image/png", png)
	theirs := e.upload("other", "theirs.png", "image/png", png)
	data := e.App.Home.Data(id)
	_ = os.MkdirAll(filepath.Join(data, "charts"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "charts", "today 1.pdf"), []byte("%PDF-1.4 fake"), 0o600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(data), "secret.txt"), []byte("top-secret-content"), 0o600)
	_ = os.Symlink(filepath.Join(filepath.Dir(data), "secret.txt"), filepath.Join(data, "link.txt"))
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments", "records": jsonl(
		map[string]any{"title": "a scan", "file": mine}, map[string]any{"title": "b theirs", "file": theirs},
		map[string]any{"title": "c chart", "file": "charts/today 1.pdf"}, map[string]any{"title": "d escape", "file": "../secret.txt"},
		map[string]any{"title": "e link", "file": "link.txt"}, map[string]any{"title": "f gone", "file": "nope.pdf"})}, nil)
	check.messages(t, e.screenMessages("btc", "files", nil))
	got := e.screenData("btc", "files", nil)["b0"]
	var files []map[string]any
	_ = json.Unmarshal(got["files"], &files)
	if string(got["state"]) != `"ready"` || len(files) != 6 {
		t.Fatalf("media shows a line per item: %s %s", got["state"], got["files"])
	}
	if f := files[0]; f["title"] != "a scan" || f["name"] != "scan.png" || f["type"] != "image/png" || f["url"] != fmt.Sprintf("/api/files/%d", mine) || f["error"] != nil {
		t.Fatalf("a stored file of the project is shown: %v", f)
	}
	if f := files[2]; f["name"] != "today 1.pdf" || f["type"] != "application/pdf" || f["url"] != "/api/files/data/btc/charts/today%201.pdf" {
		t.Fatalf("a file of the data folder is shown: %v", f)
	}
	for i, want := range map[int]string{1: fmt.Sprintf("no file %d in btc", theirs), 3: `no file "../secret.txt"`, 4: `no file "link.txt"`, 5: `no file "nope.pdf"`} {
		if f := files[i]; f["url"] != nil || !strings.Contains(fmt.Sprint(f["error"]), want) {
			t.Fatalf("line %d is an error of its own (%s): %v", i, want, f)
		}
	}
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", e.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(files[2]["url"].(string)); code != 200 || body != "%PDF-1.4 fake" {
		t.Fatalf("the url of a data file gives the file: %d %q", code, body)
	}
	if code, body := get(files[0]["url"].(string)); code != 200 || body != png {
		t.Fatalf("the url of a stored file gives the file: %d %q", code, body)
	}
	for _, path := range []string{"/api/files/data/btc/..%2Fsecret.txt", "/api/files/data/btc/link.txt", "/api/files/data/nowhere/charts/today%201.pdf"} {
		if code, body := get(path); code != 404 || strings.Contains(body, "top-secret-content") {
			t.Fatalf("%s gives nothing out of the data folder of a project: %d %q", path, code, body)
		}
	}
	_, other := e.token("other", "other.*")
	req, _ := http.NewRequest("GET", e.URL+files[2]["url"].(string), nil)
	req.Header.Set("Authorization", "Bearer "+other)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode == 200 {
		t.Fatalf("a token of another project gets no data file of this one: %v %v", resp.StatusCode, err)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"item": {"file": "file", "title": "title"}`, `"item": {"file": "path", "title": "title"}`, `unknown field "path"`},
		{`"item": {"file": "file", "title": "title"}`, `"item": {"title": "title"}`, `missing property 'file'`},
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

// TestAPipeInTheDataFolderDoesNotHangMedia: a named pipe in the data folder is no file of a media line.
func TestAPipeInTheDataFolderDoesNotHangMedia(t *testing.T) {
	e := newEnv(t, nil)
	id := e.createProject("btc", forecastManifest)
	data := e.App.Home.Data(id)
	_ = os.MkdirAll(data, 0o700)
	if err := syscall.Mkfifo(filepath.Join(data, "pipe.png"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments", "records": jsonl(map[string]any{"title": "pipe", "file": "pipe.png"})}, nil)
	done := make(chan []map[string]any, 1)
	go func() {
		var files []map[string]any
		_ = json.Unmarshal(e.screenData("btc", "files", nil)["b0"]["files"], &files)
		done <- files
	}()
	select {
	case files := <-done:
		if len(files) != 1 || !strings.Contains(fmt.Sprint(files[0]["error"]), `no file "pipe.png"`) {
			t.Fatalf("a pipe is no file: %v", files)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a pipe in the data folder hangs the screen")
	}
}

// TestTheCockpitHidesRunningNowWhenNothingRuns: the list of running processes is on the cockpit only while one
// runs.
func TestTheCockpitHidesRunningNowWhenNothingRuns(t *testing.T) {
	hold := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return provider.Result{Text: "{}"}
	})
	comps := func() string { return string(e.screenMessages("", "cockpit", nil)[1]["updateComponents"]) }
	if strings.Contains(comps(), `"title":"Running now"`) {
		t.Fatalf("nothing runs, so no list of what runs: %s", comps())
	}
	e.createProject("web", boardWebManifest)
	e.waitState(e.start("web", "score", map[string]any{"title": "x"}), "running")
	if !strings.Contains(comps(), `"title":"Running now"`) {
		t.Fatalf("a running process shows the list: %s", comps())
	}
	close(hold)
}
