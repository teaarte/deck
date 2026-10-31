package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

const sideManifest = `{"version": 13, "i18n": {"ru": {"Trades": "Сделки", "Coin": "Монета", "Side": "Сторона", "long": "в рост", "short": "в падение"}},
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

// A filter of the side panel of a screen sets a param of the screen, and every block that reads it in its where is
// narrowed by it; the filter offers the values its source has, in the owner's words.
func TestASideFilterNarrowsTheBlocksOfItsScreen(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("lab", sideManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "trades", "records": jsonl(
		map[string]any{"title": "a", "coin": "BTC", "side": "long"}, map[string]any{"title": "b", "coin": "ETH", "side": "short"},
		map[string]any{"title": "c", "coin": "BTC", "side": "short"})}, nil)

	msgs := e.screenMessages("lab", "lab", map[string]any{"lang": "ru"})
	raw, _ := json.Marshal(msgs)
	if !strings.Contains(string(raw), `"side":["s0","s1","s2","s3"]`) || !strings.Contains(string(raw), `"component":"filter"`) {
		t.Fatalf("the root of the screen names the blocks of its side panel: %s", raw)
	}
	all := e.screenData("lab", "lab", map[string]any{"lang": "ru"})
	type filter struct {
		Field, Label, Value string
		Options, Labels     []string
	}
	coin := blockData[[]filter](t, all["s0"]["filters"])
	if len(coin) != 1 || coin[0].Field != "coin" || coin[0].Label != "Монета" || strings.Join(coin[0].Options, ",") != "BTC,ETH" || coin[0].Value != "" {
		t.Fatalf("a filter offers the values of its field: %+v", coin)
	}
	side := blockData[[]filter](t, all["s1"]["filters"])
	if strings.Join(side[0].Options, ",") != "long,short" || strings.Join(side[0].Labels, ",") != "в рост,в падение" {
		t.Fatalf("the options of a filter are said by the i18n of the module: %+v", side)
	}
	if rows := blockData[[]screenRow](t, all["b0"]["rows"]); len(rows) != 3 {
		t.Fatalf("no pick, every row: %+v", rows)
	}

	picked := e.screenData("lab", "lab", map[string]any{"params": `{"coin": "BTC", "side": "short"}`})
	if rows := blockData[[]screenRow](t, picked["b0"]["rows"]); len(rows) != 1 {
		t.Fatalf("the picks narrow the table: %+v", rows)
	}
	if v := string(picked["s2"]["value"]); v != "2" {
		t.Fatalf("a small block of the panel reads the param too: %s", v)
	}
	if coin := blockData[[]filter](t, picked["s0"]["filters"]); coin[0].Value != "BTC" {
		t.Fatalf("the filter shows its pick: %+v", coin)
	}
}

// The side panel holds only filters, buttons and small blocks; a filter sets a param the screen declares and stands
// nowhere else.
func TestTheSidePanelHoldsFiltersButtonsAndSmallBlocks(t *testing.T) {
	t.Parallel()
	e := New(t)
	for _, c := range []struct{ from, to, want string }{
		{`{"type": "actions", "actions": [{"label": "All coins", "screen": "lab"}]}]`,
			`{"type": "table", "source": {"collection": "trades"}, "columns": [{"label": "Title", "bind": "title"}]}]`, "the side panel holds filter, actions, stat, list blocks, not table"},
		{`"params": {"coin": "string", "side": "string"}`, `"params": {"coin": "string"}`, `the filter sets param "side"`},
		{`"options": ["long", "short"]}`, `"options": ["long", "short"], "field": "side"}`, "not both"},
		{`"blocks": [{"type": "table"`, `"blocks": [{"type": "filter", "param": "coin", "options": ["BTC"]}, {"type": "table"`, "a filter stands in side of its screen"},
		{`"field": "coin"}`, `"field": "price"}`, `"price" is no field of its source`},
		{`"param": "side", "options"`, `"param": "tab", "options"`, "tab is a param only the client reads"},
	} {
		bad := strings.Replace(sideManifest, c.from, c.to, 1)
		if bad == sideManifest {
			t.Fatalf("the case did not apply: %s", c.from)
		}
		e.Refused("deck.project_create", map[string]any{"name": "lab", "manifest": bad}, 422, c.want)
	}
}

// TestAFilterStartsOnTheDefaultOfItsParam: a default of a param narrows the screen while the address does not name it;
// the param named empty is all; a default not of its type is refused on a save.
func TestAFilterStartsOnTheDefaultOfItsParam(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("lab", strings.Replace(sideManifest, `"params": {"coin": "string", "side": "string"}`, `"params": {"coin": {"type": "string", "default": "BTC"}, "side": "string"}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "trades", "records": jsonl(
		map[string]any{"title": "a", "coin": "BTC", "side": "long"}, map[string]any{"title": "b", "coin": "ETH", "side": "short"},
		map[string]any{"title": "c", "coin": "BTC", "side": "short"})}, nil)
	type filter struct{ Value string }
	first := e.screenData("lab", "lab", nil)
	if rows := blockData[[]screenRow](t, first["b0"]["rows"]); len(rows) != 2 || blockData[[]filter](t, first["s0"]["filters"])[0].Value != "BTC" {
		t.Fatalf("the screen starts on the default of its param: %+v", rows)
	}
	all := e.screenData("lab", "lab", map[string]any{"params": `{"coin": ""}`})
	if rows := blockData[[]screenRow](t, all["b0"]["rows"]); len(rows) != 3 || blockData[[]filter](t, all["s0"]["filters"])[0].Value != "" {
		t.Fatalf("all picked shows every row: %+v", rows)
	}
	var cur struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "lab"}, &cur)
	if !strings.Contains(cur.Manifest, `"default": "BTC"`) || !strings.Contains(cur.Manifest, `"side": "string"`) {
		t.Fatalf("a param keeps its form: %s", cur.Manifest)
	}
	bad := strings.Replace(sideManifest, `"side": "string"}`, `"side": "string", "n": {"type": "integer", "default": "many"}}`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "lab", "manifest": bad}, 422, "the default many is no integer")
}
