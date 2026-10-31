package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// LightEnv is deck with a working week behind it: modules with many records and files, runs and a long chat.
func LightEnv(t *testing.T) *Env {
	t.Helper()
	e := New(t)
	for _, name := range []string{"notes", "clips", "jobs"} {
		p := e.Project(name, strings.Replace(WebNotes, `"menu": ["main"]}`, `"menu": ["main"]}`, 1))
		var recs []map[string]any
		for i := range 200 {
			recs = append(recs, map[string]any{"title": fmt.Sprintf("%s note %03d", name, i), "source": "web", "note": strings.Repeat("words ", 20)})
		}
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "notes", "records": Jsonl(recs...)}, nil)
		for i := range 60 {
			e.Upload(name, fmt.Sprintf("file-%02d.txt", i), "text/plain", strings.Repeat("x", 100))
		}
		for i := range 20 {
			if _, err := e.App.Store.DB.Exec(`INSERT INTO process (project_id, command, kind, state, started_at, finished_at) VALUES (?, 'score', 'agent', ?, ?, ?)`,
				p, []string{"done", "error"}[i%2], time.Now().Add(-time.Duration(i)*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	return e
}

type TodayPulse struct {
	State, Line, Mark string
	Failed, Notes     int64
}

type DeskCard struct {
	ID         string
	X, Y, W, H int64
}

type deskView struct {
	Columns int64
	Own     bool
	Cards   []DeskCard
}

func (e *Env) Desk(device string, columns int) deskView {
	e.T.Helper()
	var v deskView
	e.Must("deck.layout", map[string]any{"device": device, "columns": columns}, &v)
	return v
}

func (e *Env) Place(device string, columns int, cards ...DeskCard) {
	e.T.Helper()
	raw, _ := json.Marshal(cards)
	e.Must("deck.layout_set", map[string]any{"device": device, "columns": columns, "cards": string(raw)}, nil)
}

type MenuView struct {
	Project string
	Icon    string
	Screens []struct{ Name, Title, Icon string }
	Open    *struct{ Name string }
	Running int64
	Asks    int64
	Errors  int64
}

// SideMenu is the projects of the side list of deck.menu by name, in their order, and the master's project.
func (e *Env) SideMenu() ([]string, string) {
	e.T.Helper()
	var menu struct {
		Projects []struct{ Project string }
		Master   *struct{ Project string }
	}
	e.Must("deck.menu", map[string]any{}, &menu)
	names := []string{}
	for _, p := range menu.Projects {
		names = append(names, p.Project)
	}
	if menu.Master == nil {
		return names, ""
	}
	return names, menu.Master.Project
}

// WidgetsOfWork is a module whose views mix numbers, lists with buttons, a title too long for one cell and two lists
// with their titles, as the owner's modules do.
var WidgetsOfWork = Manifest(`{"i18n": {"ru": {"Laboratory of setups": "Лаборатория сетапов", "Price": "Цена BTC", "Open": "Открыть", "Needs you": "Ждёт вас", "New": "Новое"}},
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
  ]}`)

type ScreenRow struct {
	ID      any              `json:"id"`
	Cells   []any            `json:"cells"`
	Details []any            `json:"details"`
	Tones   []any            `json:"tones"`
	Actions []map[string]any `json:"actions"`
}

// ScreenMessages resolves a screen of a project through deck.screen.
func (e *Env) ScreenMessages(project, screen string, extra map[string]any) []map[string]json.RawMessage {
	e.T.Helper()
	in := map[string]any{"screen": screen}
	if project != "" {
		in["project"] = project
	}
	for k, v := range extra {
		in[k] = v
	}
	var msgs []map[string]json.RawMessage
	e.Must("deck.screen", in, &msgs)
	return msgs
}

// ScreenData is the data model of a resolved screen, by block id; a whole read reads the tabs not shown too, each
// by its block, as the client does when the owner shows it.
func (e *Env) ScreenData(project, screen string, extra map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	msgs := e.ScreenMessages(project, screen, extra)
	out := DataOf(e.T, msgs)
	if extra["topic"] != nil || extra["block"] != nil {
		return out
	}
	for _, panel := range TabPanels(msgs) {
		maps.Copy(out, DataOf(e.T, e.ScreenMessages(project, screen, Merge(extra, map[string]any{"block": panel[0], "tab": panel[1]}))))
	}
	return out
}

// EveryTab are the messages of a screen read with each of its outermost tabs shown, the first as it is.
func (e *Env) EveryTab(project, screen string, extra map[string]any) [][]map[string]json.RawMessage {
	e.T.Helper()
	first := e.ScreenMessages(project, screen, extra)
	out := [][]map[string]json.RawMessage{first}
	for _, panel := range TabPanels(first)[min(1, len(TabPanels(first))):] {
		out = append(out, e.ScreenMessages(project, screen, Merge(extra, map[string]any{"tab": panel[1]})))
	}
	return out
}

// TabPanels are the ids and keys of the panels of the outermost tabs of a screen.
func TabPanels(msgs []map[string]json.RawMessage) [][2]string {
	var out [][2]string
	for _, m := range msgs {
		var uc struct{ Components []map[string]any }
		if json.Unmarshal(m["updateComponents"], &uc) != nil {
			continue
		}
		for _, c := range uc.Components {
			id, _ := c["id"].(string)
			key, _ := c["key"].(string)
			if c["component"] == "screen" && key != "" && !slices.ContainsFunc(out, func(p [2]string) bool { return strings.HasPrefix(id, p[0]+"-") }) {
				out = append(out, [2]string{id, key})
			}
		}
	}
	return out
}

// DataOf merges the data messages of a screen, one per block path, by block id.
func DataOf(t testing.TB, msgs []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, m := range msgs {
		raw, ok := m["updateDataModel"]
		if !ok {
			continue
		}
		var dm struct {
			Path  string                     `json:"path"`
			Value map[string]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &dm); err != nil {
			t.Fatalf("the data of a screen: %v", err)
		}
		out[strings.TrimPrefix(dm.Path, "/")] = dm.Value
	}
	return out
}

// ScreenRows returns the rows of the first table block of a screen.
func (e *Env) ScreenRows(project int64, screen string) []ScreenRow {
	e.T.Helper()
	var rows []ScreenRow
	if err := json.Unmarshal(e.ScreenData(e.Names[project], screen, nil)["b0"]["rows"], &rows); err != nil {
		e.T.Fatalf("the rows of screen %s: %v", screen, err)
	}
	return rows
}

type FormV struct {
	Project  string `json:"project"`
	Commands []struct {
		Name    string `json:"name"`
		Project string `json:"project"`
		Input   struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		} `json:"input"`
		Order  []string       `json:"order"`
		Result map[string]any `json:"result"`
	} `json:"commands"`
	Values   map[string]any `json:"values"`
	Readonly []string       `json:"readonly"`
	Send     map[string]any `json:"send"`
	Record   int64          `json:"record"`
}

// RowAction is the first button of a screen that calls command, any for "", with params holding match.
func (e *Env) RowAction(project, screen, command string, match map[string]any) map[string]any {
	e.T.Helper()
	return e.Button(project, screen, nil, command, match)
}

// Button is rowAction on a screen opened with extra input of deck.screen, such as its params.
func (e *Env) Button(project, screen string, extra map[string]any, command string, match map[string]any) map[string]any {
	e.T.Helper()
	var find func(v any) map[string]any
	find = func(v any) map[string]any {
		switch v := v.(type) {
		case map[string]any:
			params, _ := v["params"].(map[string]any)
			held := command == "" || v["command"] == command
			for k, want := range match {
				held = held && fmt.Sprint(params[k]) == fmt.Sprint(want)
			}
			if held {
				return v
			}
			for _, x := range v {
				if a := find(x); a != nil {
					return a
				}
			}
		case []any:
			for _, x := range v {
				if a := find(x); a != nil {
					return a
				}
			}
		}
		return nil
	}
	b, _ := json.Marshal(e.ScreenData(project, screen, extra))
	var v any
	_ = json.Unmarshal(b, &v)
	a := find(v)
	if a == nil {
		e.T.Fatalf("no button of %s.%s calls %s", project, screen, command)
	}
	return a
}

// Press calls the command of a button with its params, as the client does.
func (e *Env) Press(a map[string]any) (int, []byte) {
	e.T.Helper()
	params, _ := a["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	return e.Call(a["command"].(string), params)
}

// catalogCheck validates resolved components and block data against schemas/catalog.json.
type catalogCheck struct {
	c     *jsonschema.Compiler
	data  *jsonschema.Schema
	Comps map[string]string
	// basic are the standard components of A2UI the catalog draws with; the stock renderer checks their props.
	basic []string
}

func NewCatalogCheck(t *testing.T) *catalogCheck {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(Root, "schemas/catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	common, _ := jsonschema.UnmarshalJSON(strings.NewReader(`{"$defs": {"ChildList": {"type": "array", "items": {"type": "string"}},
		"ComponentId": {"type": "string"}, "DynamicValue": {}, "DynamicString": {}, "Action": {"type": "object"}}}`))
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://deck2.test/catalog.json", doc); err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("https://deck2.test/common_types.json", common); err != nil {
		t.Fatal(err)
	}
	data, err := c.Compile("https://deck2.test/catalog.json#/$defs/data")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct{ Basic struct{ Components []string } }
	_ = json.Unmarshal(raw, &cat)
	return &catalogCheck{c: c, data: data, Comps: map[string]string{}, basic: cat.Basic.Components}
}

type ListItem struct {
	ID        any              `json:"id"`
	Title     string           `json:"title"`
	Text      string           `json:"text"`
	Meta      string           `json:"meta"`
	Since     string           `json:"since"`
	Details   string           `json:"details"`
	Questions []map[string]any `json:"questions"`
	Actions   []map[string]any `json:"actions"`
	Group     string           `json:"group"`
	Tone      string           `json:"tone"`
}

// AllModels are the cards of the models of Settings, in their order.
func (e *Env) AllModels(extra map[string]any) []ListItem {
	e.T.Helper()
	return BlockData[[]ListItem](e.T, e.SettingsBlock("All models", extra)["items"])
}

func (e *Env) CockpitItems() []ListItem {
	e.T.Helper()
	var items []ListItem
	_ = json.Unmarshal(e.ScreenData("", "cockpit", nil)["b0"]["items"], &items)
	return items
}

func (e *Env) CockpitRunning() []ListItem {
	e.T.Helper()
	var items []ListItem
	_ = json.Unmarshal(e.ScreenData("", "cockpit", nil)["b1"]["items"], &items)
	return items
}

// DialogsOf are the inputs of deck.screen that open each dialog of a screen.
func (e *Env) DialogsOf(project, screen string) []map[string]any {
	e.T.Helper()
	var out []map[string]any
	for _, m := range e.ScreenMessages(project, screen, nil) {
		var uc struct{ Components []map[string]any }
		_ = json.Unmarshal(m["updateComponents"], &uc)
		for _, c := range uc.Components {
			if name, _ := c["name"].(string); c["component"] == "screen" && name != "" {
				out = append(out, map[string]any{"dialog": name})
			}
		}
	}
	return out
}

func (k *catalogCheck) Messages(t *testing.T, msgs []map[string]json.RawMessage) {
	t.Helper()
	for _, m := range msgs {
		if string(m["version"]) != `"v0.9"` {
			t.Fatalf("every message carries protocol v0.9: %s", m["version"])
		}
		if raw, ok := m["updateComponents"]; ok {
			var uc struct{ Components []map[string]any }
			_ = json.Unmarshal(raw, &uc)
			for _, comp := range uc.Components {
				name := comp["component"].(string)
				k.Comps[comp["id"].(string)] = name
				if slices.Contains(k.basic, name) {
					continue
				}
				s, err := k.c.Compile("https://deck2.test/catalog.json#/components/" + name)
				if err != nil {
					t.Fatalf("component %s is not in the catalog: %v", name, err)
				}
				props := map[string]any{}
				for key, v := range comp {
					if key != "id" && key != "component" {
						props[key] = v
					}
					// A block that reads data at other paths too, as a chat its parts, has its value at each.
					if m, ok := v.(map[string]any); ok && key != "data" && m["path"] != nil {
						at, _ := m["path"].(string)
						k.Comps[strings.TrimPrefix(at, "/")] = name
					}
				}
				if err := s.Validate(roundTrip(props)); err != nil {
					t.Fatalf("component %s does not match the catalog: %v", comp["id"], err)
				}
			}
		}
		if raw, ok := m["updateDataModel"]; ok {
			var dm struct {
				Path  string
				Value any
			}
			_ = json.Unmarshal(raw, &dm)
			values := map[string]any{dm.Path: dm.Value}
			for id, v := range values {
				if err := k.data.Validate(v); err != nil {
					t.Fatalf("data of %s does not match the catalog: %v", id, err)
				}
				value, ok := v.(map[string]any)["value"]
				if !ok {
					continue
				}
				s, err := k.c.Compile("https://deck2.test/catalog.json#/$defs/values/" + k.Comps[strings.TrimPrefix(id, "/")])
				if err != nil {
					t.Fatalf("the catalog has no value of block %s (%s): %v", id, k.Comps[strings.TrimPrefix(id, "/")], err)
				}
				if err := s.Validate(value); err != nil {
					t.Fatalf("value of %s does not match the catalog: %v", id, err)
				}
			}
		}
	}
}

// SettingsBlock is the data of the block of deck's settings with this title, found by its component.
func (e *Env) SettingsBlock(title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	return e.ScreenBlock("", "settings", title, extra)
}

// ScreenBlock is the data of the block of a screen with this title, found by its component in the tab it is in.
func (e *Env) ScreenBlock(project, screen, title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	id, shown := e.FindBlock(project, screen, title)
	if id == "" {
		e.T.Fatalf("%s has no block %q", screen, title)
	}
	extra = Merge(extra, shown)
	if extra["topic"] == nil {
		return e.ScreenData(project, screen, extra)[id]
	}
	for _, m := range e.ScreenMessages(project, screen, extra) {
		var dm struct {
			Path  string
			Value map[string]json.RawMessage
		}
		if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path == "/"+id {
			return dm.Value
		}
	}
	e.T.Fatalf("topic %v does not read block %q", extra["topic"], title)
	return nil
}

// FindBlock is the id of the block of a screen with this title and the tabs and dialog that show it: a block shown by a when in
// a tab is drawn only with its tab shown.
func (e *Env) FindBlock(project, screen, title string) (string, map[string]any) {
	e.T.Helper()
	comps := func(extra map[string]any) []map[string]any {
		var out []map[string]any
		for _, m := range e.ScreenMessages(project, screen, extra) {
			var uc struct{ Components []map[string]any }
			if json.Unmarshal(m["updateComponents"], &uc) == nil {
				out = append(out, uc.Components...)
			}
		}
		return out
	}
	all := comps(nil)
	shown := func(drawn []map[string]any, id string) map[string]any {
		out := map[string]any{}
		n := 0
		for _, c := range drawn {
			p, _ := c["id"].(string)
			if name, _ := c["name"].(string); name != "" {
				p = strings.TrimSuffix(p, "-in")
			}
			if c["component"] != "screen" || id != p && !strings.HasPrefix(id, p+"-") {
				continue
			}
			if c["key"] != nil {
				out[[]string{"tab", "sub"}[min(n, 1)]] = c["key"]
				n++
			} else if name, _ := c["name"].(string); name != "" {
				out["dialog"] = name
			}
		}
		return out
	}
	find := func(cs []map[string]any) string {
		for _, c := range cs {
			if c["title"] == title {
				return c["id"].(string)
			}
		}
		for _, c := range cs {
			if c["component"] == "Text" && c["text"] == title {
				return c["id"].(string)
			}
		}
		return ""
	}
	if id := find(all); id != "" {
		return id, shown(all, id)
	}
	for _, c := range all {
		if p, _ := c["id"].(string); c["component"] == "screen" && c["key"] != nil {
			drawn := comps(shown(all, p))
			if id := find(drawn); id != "" {
				return id, shown(drawn, id)
			}
		}
	}
	return "", nil
}

// ScreenForm is the data of the form of a screen that calls this command, in a dialog or not.
func (e *Env) ScreenForm(project, screen, command string) map[string]json.RawMessage {
	e.T.Helper()
	for _, extra := range append([]map[string]any{nil}, e.DialogsOf(project, screen)...) {
		for _, d := range e.ScreenData(project, screen, extra) {
			if strings.Contains(string(d["value"]), `"commands":[{`) && strings.Contains(string(d["value"]), `"name":"`+command+`"`) {
				return d
			}
		}
	}
	e.T.Fatalf("%s has no form of %s", screen, command)
	return nil
}

func FindItem(t *testing.T, items []ListItem, prefix string) ListItem {
	t.Helper()
	for _, it := range items {
		if strings.HasPrefix(it.Title, prefix) {
			return it
		}
	}
	t.Fatalf("no item %q in %+v", prefix, items)
	return ListItem{}
}

func Verb(t *testing.T, acts []map[string]any, v string) map[string]any {
	t.Helper()
	for _, a := range acts {
		if a["verb"] == v {
			return a
		}
	}
	t.Fatalf("no %s button in %+v", v, acts)
	return nil
}

func HasVerb(acts []map[string]any, v string) bool {
	return slices.ContainsFunc(acts, func(a map[string]any) bool { return a["verb"] == v })
}

type ScreenTable struct {
	Rows []ScreenRow `json:"rows"`
}
